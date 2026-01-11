//go:build e2e

package dns

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"github.com/byoungmin/kube-service-tunnel/internal/kube"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

const (
	e2eKubeconfigEnv = "KST_E2E_KUBECONFIG"
	e2eContextEnv    = "KST_E2E_CONTEXT"
	e2eImage         = "traefik/whoami:v1.10.3"
	e2eAppLabel      = "kst-e2e-web"
)

// TestScenariosKind runs every scenario against a disposable kind cluster. It runs only with `-tags e2e` and
// a kind context. Each scenario creates and deletes its own namespace, so the scenarios run in parallel.
func TestScenariosKind(t *testing.T) {
	t.Parallel()

	kubeconfig, contextName := os.Getenv(e2eKubeconfigEnv), os.Getenv(e2eContextEnv)
	if kubeconfig == "" || contextName == "" {
		t.Skipf("set %s and %s to run the kind scenarios", e2eKubeconfigEnv, e2eContextEnv)
	}
	if !strings.HasPrefix(contextName, "kind-") {
		t.Fatalf("refusing to run against %q: the kind scenarios only run on a kind context", contextName)
	}
	config, err := kube.LoadKubeconfigWithContext(kubeconfig, contextName)
	if err != nil {
		t.Fatalf("load kubeconfig: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatalf("create clientset: %v", err)
	}

	runScenarios(t, kindCluster{kubeconfig: kubeconfig, context: contextName, clientset: clientset})
}

type kindCluster struct {
	kubeconfig string
	context    string
	clientset  kubernetes.Interface
}

// newWorkload creates a namespace with a random suffix and fills it. The namespace is deleted when the
// scenario ends, without waiting for the deletion to finish.
func (c kindCluster) newWorkload(t *testing.T) workload {
	t.Helper()
	w := &kindWorkload{
		kubeconfigPath: c.kubeconfig,
		context:        c.context,
		ns:             fmt.Sprintf("kst-e2e-%08x", rand.Uint32()),
		clientset:      c.clientset,
	}
	w.create(t)
	return w
}

type kindWorkload struct {
	kubeconfigPath string
	context        string
	ns             string
	clientset      kubernetes.Interface
}

func (w *kindWorkload) kubeconfig() string  { return w.kubeconfigPath }
func (w *kindWorkload) contextName() string { return w.context }
func (w *kindWorkload) namespace() string   { return w.ns }

// awaitLossMonitors does nothing: the real API server replays the events after the resource version a
// monitor started from, so a deletion right after registering is not missed.
func (w *kindWorkload) awaitLossMonitors(*testing.T, int) {}

// awaitMonitorsStopped does nothing: a real API server offers no view of the open watches. The strict check
// for a repeated lost report is the kube package's unit test of the port forward client.
func (w *kindWorkload) awaitMonitorsStopped(*testing.T) {}

func (w *kindWorkload) webPod(t *testing.T) string {
	t.Helper()
	pods, err := w.clientset.CoreV1().Pods(w.ns).List(context.Background(),
		metav1.ListOptions{LabelSelector: "app=" + e2eAppLabel})
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	for _, pod := range pods.Items {
		if pod.DeletionTimestamp == nil && podReady(pod) {
			return pod.Name
		}
	}
	t.Fatalf("no ready web pod in %s", w.ns)
	return ""
}

func (w *kindWorkload) deleteWebPod(t *testing.T) {
	t.Helper()
	err := w.clientset.CoreV1().Pods(w.ns).DeleteCollection(context.Background(), metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: "app=" + e2eAppLabel})
	if err != nil {
		t.Fatalf("delete pods: %v", err)
	}
}

// replaceWebPod waits for the deployment to start its replacement pod.
func (w *kindWorkload) replaceWebPod(t *testing.T) string {
	t.Helper()
	w.waitDeploymentReady(t)
	return w.webPod(t)
}

func (w *kindWorkload) create(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: w.ns}}
	if _, err := w.clientset.CoreV1().Namespaces().Create(ctx, namespace, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	t.Cleanup(func() {
		_ = w.clientset.CoreV1().Namespaces().Delete(context.Background(), w.ns, metav1.DeleteOptions{})
	})

	labels := map[string]string{"app": e2eAppLabel}
	if _, err := w.clientset.AppsV1().Deployments(w.ns).Create(ctx, w.deployment(labels), metav1.CreateOptions{}); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	services := []*corev1.Service{
		w.service("web", labels, 8080, intstr.FromString("http")),
		w.service("api", labels, 80, intstr.FromInt32(80)),
		w.service("ext", nil, 80, intstr.FromInt32(80)),
		w.service("grpc", labels, 9090, intstr.FromInt32(80)),
	}
	for _, svc := range services {
		if _, err := w.clientset.CoreV1().Services(w.ns).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create service %s: %v", svc.Name, err)
		}
	}
	w.waitDeploymentReady(t)
}

func (w *kindWorkload) deployment(labels map[string]string) *appsv1.Deployment {
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: w.ns},
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
}

func (w *kindWorkload) service(name string, selector map[string]string, port int32, target intstr.IntOrString) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: w.ns},
		Spec: corev1.ServiceSpec{
			Selector: selector,
			Ports:    []corev1.ServicePort{{Name: "http", Port: port, TargetPort: target, Protocol: corev1.ProtocolTCP}},
		},
	}
}

func (w *kindWorkload) waitDeploymentReady(t *testing.T) {
	t.Helper()
	eventually(t, "deployment ready", scenarioWait, func() bool {
		deployment, err := w.clientset.AppsV1().Deployments(w.ns).Get(context.Background(), "web", metav1.GetOptions{})
		if err != nil || deployment.Status.ReadyReplicas < 1 || deployment.Status.UpdatedReplicas < 1 {
			return false
		}
		pods, err := w.clientset.CoreV1().Pods(w.ns).List(context.Background(),
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
