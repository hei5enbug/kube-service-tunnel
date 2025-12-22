package portforward

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func NewPortForwardManager(kubeconfigPath string) *PortForwardManager {
	return &PortForwardManager{
		forwards:       make(map[string]*PortForward),
		kubeconfigPath: kubeconfigPath,
		clientsets:     make(map[string]kubernetes.Interface),
		configs:        make(map[string]*rest.Config),
	}
}

