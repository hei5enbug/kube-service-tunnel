package dns

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/byoungmin/kube-service-tunnel/internal/kube"
	proxyadapter "github.com/byoungmin/kube-service-tunnel/internal/proxy"
)

// scenarioCluster is a place where scenarios run: a fake API server or a real disposable cluster.
type scenarioCluster interface {
	// newWorkload gives a scenario its own fresh workload. Workloads of different scenarios never share state.
	newWorkload(t *testing.T) workload
}

// workload is the set of services and the single ready web pod behind them that one scenario works with.
// The services are web (port 8080 to the container port named http), api (80 to 80), ext (no selector) and
// grpc (port 9090, which is not HTTP). The web pod answers every request with a whoami body that contains
// "Hostname: <pod name>".
type workload interface {
	kubeconfig() string
	contextName() string
	namespace() string
	// webPod returns the name of the ready web pod.
	webPod(t *testing.T) string
	// awaitLossMonitors blocks until the given number of forwards are watching their pod, so that deleting
	// the pod afterwards cannot go unnoticed.
	awaitLossMonitors(t *testing.T, forwards int)
	// awaitMonitorsStopped blocks until the loss monitors of the lost forwards have finished, so that no
	// further lost report can follow.
	awaitMonitorsStopped(t *testing.T)
	deleteWebPod(t *testing.T)
	// replaceWebPod blocks until a new ready web pod exists and returns its name.
	replaceWebPod(t *testing.T) string
}

type scenario struct {
	name string
	run  func(t *testing.T, env *scenarioEnv)
}

// scenarioEnv is one scenario's manager, built from the real kube adapter and the real proxy. Only the
// hosts file is in memory.
type scenarioEnv struct {
	workload  workload
	adapter   kube.KubeAdapterInterface
	manager   *DNSManager
	hosts     *fakeHosts
	proxyPort int32
	lost      chan DNSTunnel
}

// runScenarios runs every scenario as an independent parallel subtest with its own workload and manager.
func runScenarios(t *testing.T, cluster scenarioCluster) {
	t.Helper()
	for _, sc := range scenarios() {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			sc.run(t, newScenarioEnv(t, cluster))
		})
	}
}

func newScenarioEnv(t *testing.T, cluster scenarioCluster) *scenarioEnv {
	t.Helper()
	w := cluster.newWorkload(t)
	adapter, err := kube.NewKubeAdapter(w.kubeconfig())
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}
	env := &scenarioEnv{
		workload:  w,
		adapter:   adapter,
		hosts:     newFakeHosts(),
		proxyPort: freePort(t),
		lost:      make(chan DNSTunnel, 32),
	}
	proxy := remappedProxy{ProxyAdapterInterface: proxyadapter.NewProxyAdapter(), port: env.proxyPort}
	env.manager = newDNSManager(adapter, env.hosts, proxy)
	env.manager.SetTunnelLostHandler(func(tunnel DNSTunnel, _ error) { env.lost <- tunnel })
	t.Cleanup(func() { _ = env.manager.Cleanup() })
	return env
}

func scenarios() []scenario {
	return []scenario{
		{"클러스터에서 서비스와 등록 가능 여부를 읽는다", discoversServices},
		{"등록하면 프록시와 로컬 포트 양쪽으로 pod에 접속된다", reachesPodAfterRegister},
		{"같은 서비스를 다시 등록하면 거절한다", refusesSecondRegistration},
		{"호스트명과 로컬 포트를 바꾸면 새 값으로만 접속된다", reachesPodOnlyByUpdatedSettings},
		{"일괄 등록은 등록 가능한 서비스만 등록하고 나머지는 이유를 남긴다", bulkRegistersEligibleOnly},
		{"pod가 지워지면 그 pod의 터널을 모두 자동으로 제거한다", removesTunnelsOfDeletedPod},
		{"새 pod가 준비되면 기억한 호스트명과 포트로 다시 등록된다", registersAgainOnReplacementPod},
		{"정리하면 프록시와 포트 포워드와 hosts 항목이 모두 사라진다", cleanupRemovesEverything},
	}
}

// target names a service of the scenario's namespace. Each scenario picks its own free local port, because
// scenarios run side by side and the automatic ports of all of them would start at the same number.
func (e *scenarioEnv) target(t *testing.T, name string) kube.ServiceRef {
	t.Helper()
	return kube.ServiceRef{Namespace: e.workload.namespace(), Name: name, LocalPort: freePort(t)}
}

func (e *scenarioEnv) register(t *testing.T, target kube.ServiceRef) DNSTunnel {
	t.Helper()
	tunnel, err := e.manager.RegisterDNSTunnel(context.Background(), e.workload.contextName(), target)
	if err != nil {
		t.Fatalf("register %s/%s: %v", target.Namespace, target.Name, err)
	}
	return tunnel
}

// awaitLost waits for want lost-tunnel reports, one per tunnel, and fails on a repeated or extra report.
func (e *scenarioEnv) awaitLost(t *testing.T, want int) map[string]DNSTunnel {
	t.Helper()
	lost := map[string]DNSTunnel{}
	timeout := time.After(scenarioWait)
	for len(lost) < want {
		select {
		case tunnel := <-e.lost:
			if _, repeated := lost[tunnel.DNSURL]; repeated {
				t.Fatalf("tunnel %s reported lost twice", tunnel.DNSURL)
			}
			lost[tunnel.DNSURL] = tunnel
		case <-timeout:
			t.Fatalf("lost tunnels = %v, want %d", lost, want)
		}
	}
	e.workload.awaitMonitorsStopped(t)
	select {
	case extra := <-e.lost:
		t.Fatalf("unexpected extra lost report for %s", extra.DNSURL)
	default:
	}
	return lost
}

// webAndApi is the pair of tunnels of the pod-loss scenarios: web with its settings after the update, the old
// local port it had before, and api from the bulk registration.
type webAndApi struct {
	web     DNSTunnel
	oldPort int32
	api     DNSTunnel
}

// registerWebAndApi registers web, updates it to the custom hostname web.local on a new free local port,
// bulk-registers api, and waits until both forwards watch their pod.
func (e *scenarioEnv) registerWebAndApi(t *testing.T) webAndApi {
	t.Helper()
	registered := e.register(t, e.target(t, "web"))
	web, err := e.manager.UpdateTunnel(context.Background(), registered.DNSURL, "web.local", freePort(t))
	if err != nil {
		t.Fatalf("update web: %v", err)
	}
	result, err := e.manager.RegisterServices(context.Background(), e.workload.contextName(), []kube.ServiceRef{e.target(t, "api")}, nil)
	if err != nil || len(result.Registered) != 1 || len(result.Failures) != 0 {
		t.Fatalf("bulk register api: %+v, err = %v", result, err)
	}
	e.workload.awaitLossMonitors(t, 2)
	return webAndApi{web: web, oldPort: registered.LocalPort, api: result.Registered[0]}
}

func discoversServices(t *testing.T, env *scenarioEnv) {
	// given
	namespace := env.workload.namespace()

	// when
	resources := env.adapter.FetchContextResources(context.Background(), env.workload.contextName())

	// then
	if resources.Err != nil {
		t.Fatalf("discovery error: %v", resources.Err)
	}
	reasons := map[string]string{}
	for _, svc := range resources.Namespaces[namespace] {
		_, reason := kube.Eligibility(&svc)
		reasons[svc.Name] = reason
	}
	want := map[string]string{"web": "", "api": "", "ext": kube.ReasonNoSelector, "grpc": kube.ReasonNoHTTPPort}
	for name, reason := range want {
		if got, ok := reasons[name]; !ok || got != reason {
			t.Fatalf("service %s reason = %q (listed %v), want %q", name, got, ok, reason)
		}
	}
}

func reachesPodAfterRegister(t *testing.T, env *scenarioEnv) {
	// given
	target := env.target(t, "web")
	pod := env.workload.webPod(t)

	// when
	tunnel, err := env.manager.RegisterDNSTunnel(context.Background(), env.workload.contextName(), target)

	// then
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if tunnel.DNSURL != "web."+target.Namespace || !env.hosts.has(tunnel.DNSURL) {
		t.Fatalf("tunnel = %+v, hosts entry %v", tunnel, env.hosts.has(tunnel.DNSURL))
	}
	expectPod(t, env.proxyPort, tunnel.DNSURL, pod)
	expectPod(t, tunnel.LocalPort, "", pod)
}

func refusesSecondRegistration(t *testing.T, env *scenarioEnv) {
	// given
	env.register(t, env.target(t, "web"))
	again := env.target(t, "web")
	again.Hostname = "other.local"

	// when
	_, err := env.manager.RegisterDNSTunnel(context.Background(), env.workload.contextName(), again)

	// then
	if err == nil || !strings.Contains(err.Error(), "already registered as") {
		t.Fatalf("err = %v, want already registered", err)
	}
}

func reachesPodOnlyByUpdatedSettings(t *testing.T, env *scenarioEnv) {
	// given
	old := env.register(t, env.target(t, "web"))
	newPort := freePort(t)
	pod := env.workload.webPod(t)

	// when
	updated, err := env.manager.UpdateTunnel(context.Background(), old.DNSURL, "web.local", newPort)

	// then
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.DNSURL != "web.local" || updated.LocalPort != newPort {
		t.Fatalf("updated = %+v", updated)
	}
	expectPod(t, env.proxyPort, "web.local", pod)
	expectPod(t, newPort, "", pod)
	expectStatus(t, env.proxyPort, old.DNSURL, http.StatusNotFound)
	eventually(t, "old local port closed", portWait, func() bool { return !portOpen(old.LocalPort) })
	if env.hosts.has(old.DNSURL) || !env.hosts.has("web.local") {
		t.Fatalf("hosts entries not switched")
	}
}

func bulkRegistersEligibleOnly(t *testing.T, env *scenarioEnv) {
	// given
	targets := []kube.ServiceRef{env.target(t, "api"), env.target(t, "ext"), env.target(t, "grpc")}
	pod := env.workload.webPod(t)

	// when
	result, err := env.manager.RegisterServices(context.Background(), env.workload.contextName(), targets, nil)

	// then
	if err != nil || len(result.Registered) != 1 || len(result.Ineligible) != 2 || len(result.Failures) != 0 {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	reasons := map[string]string{}
	for _, ineligible := range result.Ineligible {
		reasons[ineligible.Service] = ineligible.Reason
	}
	if reasons["ext"] != kube.ReasonNoSelector || reasons["grpc"] != kube.ReasonNoHTTPPort {
		t.Fatalf("ineligible reasons = %v", reasons)
	}
	expectPod(t, env.proxyPort, "api."+env.workload.namespace(), pod)
}

func removesTunnelsOfDeletedPod(t *testing.T, env *scenarioEnv) {
	// given
	tunnels := env.registerWebAndApi(t)

	// when
	env.workload.deleteWebPod(t)
	lost := env.awaitLost(t, 2)

	// then
	_, webLost := lost[tunnels.web.DNSURL]
	_, apiLost := lost[tunnels.api.DNSURL]
	if !webLost || !apiLost {
		t.Fatalf("lost tunnels = %v, want %s and %s", lost, tunnels.web.DNSURL, tunnels.api.DNSURL)
	}
	if remaining := env.manager.GetAllDNSTunnels(); len(remaining) != 0 {
		t.Fatalf("remaining tunnels = %+v", remaining)
	}
	expectStatus(t, env.proxyPort, tunnels.web.DNSURL, http.StatusNotFound)
	expectStatus(t, env.proxyPort, tunnels.api.DNSURL, http.StatusNotFound)
	if env.hosts.has(tunnels.web.DNSURL) || env.hosts.has(tunnels.api.DNSURL) {
		t.Fatalf("hosts entries left after loss")
	}
	ports := map[string]int32{"old web": tunnels.oldPort, "new web": tunnels.web.LocalPort, "api": tunnels.api.LocalPort}
	for name, port := range ports {
		eventually(t, name+" local port closed", portWait, func() bool { return !portOpen(port) })
	}
}

func registersAgainOnReplacementPod(t *testing.T, env *scenarioEnv) {
	// given
	tunnels := env.registerWebAndApi(t)
	env.workload.deleteWebPod(t)
	env.awaitLost(t, 2)
	replacement := env.workload.replaceWebPod(t)
	webPort := tunnels.web.LocalPort
	target := kube.ServiceRef{Namespace: env.workload.namespace(), Name: "web", Hostname: tunnels.web.DNSURL, LocalPort: webPort}

	// when
	tunnel, err := env.manager.RegisterDNSTunnel(context.Background(), env.workload.contextName(), target)

	// then
	if err != nil || tunnel.LocalPort != webPort || tunnel.DNSURL != tunnels.web.DNSURL {
		t.Fatalf("tunnel = %+v, err = %v", tunnel, err)
	}
	expectPod(t, env.proxyPort, "web.local", replacement)
	expectPod(t, webPort, "", replacement)
}

func cleanupRemovesEverything(t *testing.T, env *scenarioEnv) {
	// given
	web := env.register(t, env.target(t, "web"))
	api := env.register(t, env.target(t, "api"))

	// when
	err := env.manager.Cleanup()

	// then
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	eventually(t, "proxy stopped", portWait, func() bool { return !portOpen(env.proxyPort) })
	eventually(t, "web forward stopped", portWait, func() bool { return !portOpen(web.LocalPort) })
	eventually(t, "api forward stopped", portWait, func() bool { return !portOpen(api.LocalPort) })
	if len(env.manager.GetAllDNSTunnels()) != 0 || env.hosts.has(web.DNSURL) || env.hosts.has(api.DNSURL) {
		t.Fatalf("state left after cleanup")
	}
}
