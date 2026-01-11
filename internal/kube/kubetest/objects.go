package kubetest

import (
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// ClusterIPService builds a ClusterIP service. Without ports it exposes port 80 mapped to container port 8080
// under the name "http".
func ClusterIPService(namespace, name string, selector map[string]string, ports ...corev1.ServicePort) corev1.Service {
	if len(ports) == 0 {
		ports = []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080), Protocol: corev1.ProtocolTCP}}
	}
	return corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.10", Selector: selector, Ports: ports},
	}
}

// RunningPod builds a Running pod with one container exposing the named port "http" on 8080. Its UID is
// "uid-<name>", its resourceVersion "100", and its Ready condition follows ready.
func RunningPod(namespace, name string, podLabels map[string]string, ready bool) corev1.Pod {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: podLabels, UID: types.UID("uid-" + name), ResourceVersion: "100"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "app", Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8080, Protocol: corev1.ProtocolTCP}}},
		}},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}},
		},
	}
}

// WriteKubeconfig writes a kubeconfig into a temporary directory and returns its path. servers maps each
// context name to the API server URL it points at; the context uses cluster "cluster-<name>" and user
// "user-<name>". current becomes the current context and may be empty.
func WriteKubeconfig(tb testing.TB, current string, servers map[string]string) string {
	tb.Helper()
	config := clientcmdapi.NewConfig()
	for name, server := range servers {
		config.Clusters["cluster-"+name] = &clientcmdapi.Cluster{Server: server}
		config.AuthInfos["user-"+name] = &clientcmdapi.AuthInfo{}
		config.Contexts[name] = &clientcmdapi.Context{Cluster: "cluster-" + name, AuthInfo: "user-" + name}
	}
	config.CurrentContext = current

	path := filepath.Join(tb.TempDir(), "config")
	if err := clientcmd.WriteToFile(*config, path); err != nil {
		tb.Fatalf("kubetest: write kubeconfig: %v", err)
	}
	return path
}

// Kubeconfig writes a kubeconfig with the single context contextName that points at this server.
func (s *Server) Kubeconfig(contextName string) string {
	s.tb.Helper()
	return WriteKubeconfig(s.tb, contextName, map[string]string{contextName: s.URL()})
}
