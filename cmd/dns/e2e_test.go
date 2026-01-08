//go:build e2e

package dns

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/byoungmin/kube-service-tunnel/internal/kube"
	proxyadapter "github.com/byoungmin/kube-service-tunnel/internal/proxy"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

const (
	e2eKubeconfigEnv = "KST_E2E_KUBECONFIG"
	e2eContextEnv    = "KST_E2E_CONTEXT"
	e2eNamespace     = "kst-e2e"
	e2eImage         = "traefik/whoami:v1.10.3"
	e2eAppLabel      = "kst-e2e-web"
	e2eWait          = 3 * time.Minute
	e2ePoll          = 500 * time.Millisecond
	e2eHTTPTimeout   = 5 * time.Second
)

// remappedProxy runs the real proxy on a free high port instead of 80, so the test needs no root.
type remappedProxy struct {
	proxyadapter.ProxyAdapterInterface
	port int32
}

func (p remappedProxy) StartIfNotRunning(int32) error {
	return p.ProxyAdapterInterface.StartIfNotRunning(p.port)
}

type e2eEnv struct {
	contextName string
	clientset   kubernetes.Interface
	adapter     kube.KubeAdapterInterface
	manager     *DNSManager
	hosts       *fakeHosts
	proxyPort   int32
	lost        chan DNSTunnel
}

// TestE2EAgainstKind runs the manager, the real port forward and the real proxy against a disposable cluster.
// It runs only with `-tags e2e` and a kind context, and it creates and deletes its own namespace.
func TestE2EAgainstKind(t *testing.T) {
	env := setupE2E(t)
	ctx := context.Background()
	var webPort int32

	t.Run("클러스터에서 서비스와 등록 가능 여부를 읽는다", func(t *testing.T) {
		// given
		contextName := env.contextName

		// when
		resources := env.adapter.FetchContextResources(ctx, contextName)

		// then
		if resources.Err != nil {
			t.Fatalf("discovery error: %v", resources.Err)
		}
		reasons := map[string]string{}
		for _, svc := range resources.Namespaces[e2eNamespace] {
			_, reason := kube.Eligibility(&svc)
			reasons[svc.Name] = reason
		}
		want := map[string]string{"web": "", "api": "", "ext": kube.ReasonNoSelector, "grpc": kube.ReasonNoHTTPPort}
		for name, reason := range want {
			if got, ok := reasons[name]; !ok || got != reason {
				t.Fatalf("service %s reason = %q (listed %v), want %q", name, got, ok, reason)
			}
		}
	})

	t.Run("등록하면 프록시와 로컬 포트 양쪽으로 pod에 접속된다", func(t *testing.T) {
		// given
		target := kube.ServiceRef{Namespace: e2eNamespace, Name: "web"}

		// when
		tunnel, err := env.manager.RegisterDNSTunnel(ctx, env.contextName, target)

		// then
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		if tunnel.DNSURL != "web."+e2eNamespace || !env.hosts.has(tunnel.DNSURL) {
			t.Fatalf("tunnel = %+v, hosts entry %v", tunnel, env.hosts.has(tunnel.DNSURL))
		}
		expectPod(t, env.proxyPort, tunnel.DNSURL)
		expectPod(t, tunnel.LocalPort, "")
	})

	t.Run("같은 서비스를 다시 등록하면 거절한다", func(t *testing.T) {
		// given
		target := kube.ServiceRef{Namespace: e2eNamespace, Name: "web", Hostname: "other.local"}

		// when
		_, err := env.manager.RegisterDNSTunnel(ctx, env.contextName, target)

		// then
		if err == nil || !strings.Contains(err.Error(), "already registered as") {
			t.Fatalf("err = %v, want already registered", err)
		}
	})

	t.Run("호스트명과 로컬 포트를 바꾸면 새 값으로만 접속된다", func(t *testing.T) {
		// given
		old := findTunnel(t, env.manager, "web."+e2eNamespace)
		webPort = freePort(t)

		// when
		updated, err := env.manager.UpdateTunnel(ctx, old.DNSURL, "web.local", webPort)

		// then
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if updated.DNSURL != "web.local" || updated.LocalPort != webPort {
			t.Fatalf("updated = %+v", updated)
		}
		expectPod(t, env.proxyPort, "web.local")
		expectPod(t, webPort, "")
		expectStatus(t, env.proxyPort, old.DNSURL, http.StatusNotFound)
		eventually(t, "old local port closed", func() bool { return !portOpen(old.LocalPort) })
		if env.hosts.has(old.DNSURL) || !env.hosts.has("web.local") {
			t.Fatalf("hosts entries not switched")
		}
	})

	t.Run("일괄 등록은 등록 가능한 서비스만 등록하고 나머지는 이유를 남긴다", func(t *testing.T) {
		// given
		targets := []kube.ServiceRef{
			{Namespace: e2eNamespace, Name: "api"},
			{Namespace: e2eNamespace, Name: "ext"},
			{Namespace: e2eNamespace, Name: "grpc"},
		}

		// when
		result, err := env.manager.RegisterServices(ctx, env.contextName, targets, nil)

		// then
		if err != nil || len(result.Registered) != 1 || len(result.Ineligible) != 2 || len(result.Failures) != 0 {
			t.Fatalf("result = %+v, err = %v", result, err)
		}
		expectPod(t, env.proxyPort, "api."+e2eNamespace)
	})

	t.Run("pod가 지워지면 그 pod의 터널을 모두 자동으로 제거한다", func(t *testing.T) {
		// given
		before := len(env.manager.GetAllDNSTunnels())

		// when
		deleteWebPods(t, env.clientset)

		// then
		lost := map[string]bool{}
		deadline := time.After(e2eWait)
		for len(lost) < before {
			select {
			case tunnel := <-env.lost:
				lost[tunnel.DNSURL] = true
			case <-deadline:
				t.Fatalf("lost tunnels = %v, want %d", lost, before)
			}
		}
		if remaining := env.manager.GetAllDNSTunnels(); len(remaining) != 0 {
			t.Fatalf("remaining tunnels = %+v", remaining)
		}
		expectStatus(t, env.proxyPort, "web.local", http.StatusNotFound)
		if env.hosts.has("web.local") || env.hosts.has("api."+e2eNamespace) {
			t.Fatalf("hosts entries left after loss")
		}
	})

	t.Run("새 pod가 준비되면 기억한 호스트명과 포트로 다시 등록된다", func(t *testing.T) {
		// given
		waitDeploymentReady(t, env.clientset)
		target := kube.ServiceRef{Namespace: e2eNamespace, Name: "web", Hostname: "web.local", LocalPort: webPort}

		// when
		tunnel, err := env.manager.RegisterDNSTunnel(ctx, env.contextName, target)

		// then
		if err != nil || tunnel.LocalPort != webPort || tunnel.DNSURL != "web.local" {
			t.Fatalf("tunnel = %+v, err = %v", tunnel, err)
		}
		expectPod(t, env.proxyPort, "web.local")
	})

	t.Run("정리하면 프록시와 포트 포워드와 hosts 항목이 모두 사라진다", func(t *testing.T) {
		// given
		tunnel := findTunnel(t, env.manager, "web.local")

		// when
		err := env.manager.Cleanup()

		// then
		if err != nil {
			t.Fatalf("cleanup: %v", err)
		}
		eventually(t, "proxy stopped", func() bool { return !portOpen(env.proxyPort) })
		eventually(t, "forward stopped", func() bool { return !portOpen(tunnel.LocalPort) })
		if len(env.manager.GetAllDNSTunnels()) != 0 || env.hosts.has("web.local") {
			t.Fatalf("state left after cleanup")
		}
	})
}

func setupE2E(t *testing.T) *e2eEnv {
	t.Helper()
	kubeconfig, contextName := os.Getenv(e2eKubeconfigEnv), os.Getenv(e2eContextEnv)
	if kubeconfig == "" || contextName == "" {
		t.Skipf("set %s and %s to run the e2e test", e2eKubeconfigEnv, e2eContextEnv)
	}
	if !strings.HasPrefix(contextName, "kind-") {
		t.Fatalf("refusing to run against %q: the e2e test only runs on a kind context", contextName)
	}

	config, err := kube.LoadKubeconfigWithContext(kubeconfig, contextName)
	if err != nil {
		t.Fatalf("load kubeconfig: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatalf("create clientset: %v", err)
	}
	createWorkload(t, clientset)

	adapter, err := kube.NewKubeAdapter(kubeconfig)
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}
	env := &e2eEnv{
		contextName: contextName,
		clientset:   clientset,
		adapter:     adapter,
		hosts:       newFakeHosts(),
		proxyPort:   freePort(t),
		lost:        make(chan DNSTunnel, 8),
	}
	proxy := remappedProxy{ProxyAdapterInterface: proxyadapter.NewProxyAdapter(), port: env.proxyPort}
	env.manager = newDNSManager(adapter, env.hosts, proxy)
	env.manager.SetTunnelLostHandler(func(tunnel DNSTunnel, _ error) { env.lost <- tunnel })
	t.Cleanup(func() { _ = env.manager.Cleanup() })
	return env
}

func createWorkload(t *testing.T, clientset kubernetes.Interface) {
	t.Helper()
	ctx := context.Background()
	eventuallyWithin(t, "previous namespace deleted", e2eWait, func() bool {
		_, err := clientset.CoreV1().Namespaces().Get(ctx, e2eNamespace, metav1.GetOptions{})
		return apierrors.IsNotFound(err)
	})
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: e2eNamespace}}
	if _, err := clientset.CoreV1().Namespaces().Create(ctx, namespace, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	t.Cleanup(func() {
		_ = clientset.CoreV1().Namespaces().Delete(context.Background(), e2eNamespace, metav1.DeleteOptions{})
	})

	labels := map[string]string{"app": e2eAppLabel}
	replicas := int32(1)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: e2eNamespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name:  "whoami",
					Image: e2eImage,
					Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 80}},
					ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{Path: "/", Port: intstr.FromString("http")},
					}},
				}}},
			},
		},
	}
	if _, err := clientset.AppsV1().Deployments(e2eNamespace).Create(ctx, deployment, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	services := []*corev1.Service{
		service("web", labels, 8080, intstr.FromString("http")),
		service("api", labels, 80, intstr.FromInt32(80)),
		service("ext", nil, 80, intstr.FromInt32(80)),
		service("grpc", labels, 9090, intstr.FromInt32(80)),
	}
	for _, svc := range services {
		if _, err := clientset.CoreV1().Services(e2eNamespace).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create service %s: %v", svc.Name, err)
		}
	}
	waitDeploymentReady(t, clientset)
}

func service(name string, selector map[string]string, port int32, target intstr.IntOrString) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: e2eNamespace},
		Spec: corev1.ServiceSpec{
			Selector: selector,
			Ports:    []corev1.ServicePort{{Name: "http", Port: port, TargetPort: target, Protocol: corev1.ProtocolTCP}},
		},
	}
}

func waitDeploymentReady(t *testing.T, clientset kubernetes.Interface) {
	t.Helper()
	eventuallyWithin(t, "deployment ready", e2eWait, func() bool {
		deployment, err := clientset.AppsV1().Deployments(e2eNamespace).Get(context.Background(), "web", metav1.GetOptions{})
		if err != nil || deployment.Status.ReadyReplicas < 1 || deployment.Status.UpdatedReplicas < 1 {
			return false
		}
		pods, err := clientset.CoreV1().Pods(e2eNamespace).List(context.Background(),
			metav1.ListOptions{LabelSelector: "app=" + e2eAppLabel})
		if err != nil {
			return false
		}
		ready := 0
		for _, pod := range pods.Items {
			if pod.DeletionTimestamp == nil && podReady(pod) {
				ready++
			}
		}
		return ready == len(pods.Items) && ready > 0
	})
}

func podReady(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func deleteWebPods(t *testing.T, clientset kubernetes.Interface) {
	t.Helper()
	err := clientset.CoreV1().Pods(e2eNamespace).DeleteCollection(context.Background(), metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: "app=" + e2eAppLabel})
	if err != nil {
		t.Fatalf("delete pods: %v", err)
	}
}

func findTunnel(t *testing.T, manager *DNSManager, dnsURL string) DNSTunnel {
	t.Helper()
	for _, tunnel := range manager.GetAllDNSTunnels() {
		if tunnel.DNSURL == dnsURL {
			return tunnel
		}
	}
	t.Fatalf("tunnel %s not found", dnsURL)
	return DNSTunnel{}
}

// expectPod checks that a request reaches the whoami pod, through the proxy when hostHeader is set.
func expectPod(t *testing.T, port int32, hostHeader string) {
	t.Helper()
	status, body, err := get(port, hostHeader)
	if err != nil || status != http.StatusOK || !strings.Contains(body, "Hostname: web-") {
		t.Fatalf("GET 127.0.0.1:%d host %q = %d %q, err %v", port, hostHeader, status, firstLine(body), err)
	}
}

func expectStatus(t *testing.T, port int32, hostHeader string, want int) {
	t.Helper()
	status, body, err := get(port, hostHeader)
	if err != nil || status != want {
		t.Fatalf("GET 127.0.0.1:%d host %q = %d %q, err %v, want %d", port, hostHeader, status, firstLine(body), err, want)
	}
}

func get(port int32, hostHeader string) (int, string, error) {
	request, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", port), nil)
	if err != nil {
		return 0, "", err
	}
	if hostHeader != "" {
		request.Host = hostHeader
	}
	client := &http.Client{Timeout: e2eHTTPTimeout}
	response, err := client.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return response.StatusCode, string(body), err
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

func freePort(t *testing.T) int32 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick free port: %v", err)
	}
	defer listener.Close()
	return int32(listener.Addr().(*net.TCPAddr).Port)
}

func portOpen(port int32) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func eventually(t *testing.T, what string, condition func() bool) {
	t.Helper()
	eventuallyWithin(t, what, 30*time.Second, condition)
}

func eventuallyWithin(t *testing.T, what string, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(e2ePoll)
	}
	t.Fatalf("timed out waiting for %s", what)
}
