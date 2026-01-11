package dns

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byoungmin/kube-service-tunnel/internal/kube/kubetest"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	hermeticContext   = "hermetic"
	hermeticNamespace = "scenario"
)

func hermeticLabels() map[string]string { return map[string]string{"app": "web"} }

// TestScenariosHermetic runs every scenario against a fake API server, one fresh server per scenario.
// It needs no cluster, no Docker and no root.
func TestScenariosHermetic(t *testing.T) {
	t.Parallel()

	runScenarios(t, hermeticCluster{})
}

type hermeticCluster struct{}

func (hermeticCluster) newWorkload(t *testing.T) workload {
	t.Helper()
	server := kubetest.NewServer(t, kubetest.Config{
		Namespaces: []string{hermeticNamespace},
		Services:   map[string][]corev1.Service{hermeticNamespace: hermeticServices()},
	})
	w := &hermeticWorkload{
		server: server,
		config: server.Kubeconfig(hermeticContext),
	}
	w.startWebPod(t, "web-1")
	return w
}

func hermeticServices() []corev1.Service {
	servicePort := func(port int32, target intstr.IntOrString) corev1.ServicePort {
		return corev1.ServicePort{Name: "http", Port: port, TargetPort: target, Protocol: corev1.ProtocolTCP}
	}
	return []corev1.Service{
		kubetest.ClusterIPService(hermeticNamespace, "web", hermeticLabels(), servicePort(8080, intstr.FromString("http"))),
		kubetest.ClusterIPService(hermeticNamespace, "api", hermeticLabels(), servicePort(80, intstr.FromInt32(80))),
		kubetest.ClusterIPService(hermeticNamespace, "ext", nil, servicePort(80, intstr.FromInt32(80))),
		kubetest.ClusterIPService(hermeticNamespace, "grpc", hermeticLabels(), servicePort(9090, intstr.FromInt32(80))),
	}
}

type hermeticWorkload struct {
	server  *kubetest.Server
	config  string
	current string
}

func (w *hermeticWorkload) kubeconfig() string       { return w.config }
func (w *hermeticWorkload) contextName() string      { return hermeticContext }
func (w *hermeticWorkload) namespace() string        { return hermeticNamespace }
func (w *hermeticWorkload) webPod(*testing.T) string { return w.current }
func (w *hermeticWorkload) deleteWebPod(t *testing.T) {
	w.server.DeletePod(hermeticNamespace, w.current)
}

func (w *hermeticWorkload) awaitLossMonitors(t *testing.T, forwards int) {
	t.Helper()
	w.server.WaitForOpenWatches(t, forwards)
}

// awaitMonitorsStopped waits until the server holds no pod watch, which every loss monitor opens while it runs.
func (w *hermeticWorkload) awaitMonitorsStopped(t *testing.T) {
	t.Helper()
	w.server.WaitForOpenWatches(t, 0)
}

func (w *hermeticWorkload) replaceWebPod(t *testing.T) string {
	t.Helper()
	w.startWebPod(t, "web-2")
	return w.current
}

// startWebPod adds a ready pod whose port-forward streams reach a real HTTP server in whoami style.
func (w *hermeticWorkload) startWebPod(t *testing.T, name string) {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(response, "Hostname: %s\n", name)
	}))
	t.Cleanup(backend.Close)
	w.server.ForwardTo(hermeticNamespace, name, backend.Listener.Addr().String())
	pod := kubetest.RunningPod(hermeticNamespace, name, hermeticLabels(), true)
	pod.ResourceVersion = ""
	w.server.AddPod(pod)
	w.current = name
}
