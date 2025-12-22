package k8s

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func loadKubeconfig(kubeconfigPath string) (*rest.Config, error) {
	return loadKubeconfigWithContext(kubeconfigPath, "")
}

func loadKubeconfigWithContext(kubeconfigPath, contextName string) (*rest.Config, error) {
	if kubeconfigPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get user home directory: %w", err)
		}
		kubeconfigPath = filepath.Join(home, ".kube", "config")
	}

	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("kubeconfig file not found: %s", kubeconfigPath)
	}

	configOverrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		configOverrides.CurrentContext = contextName
	}

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		configOverrides,
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("build config from flags: %w", err)
	}

	return config, nil
}

func LoadKubeconfigWithContext(kubeconfigPath, contextName string) (*rest.Config, error) {
	return loadKubeconfigWithContext(kubeconfigPath, contextName)
}

