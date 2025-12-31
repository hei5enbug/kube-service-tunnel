package kube

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const sudoUserEnv = "SUDO_USER"

type userLookupFunc func(username string) (*user.User, error)

func DefaultKubeconfigPath() (string, error) {
	home, err := resolveHomeDir(os.Geteuid(), os.Getenv(sudoUserEnv), user.Lookup, os.UserHomeDir)
	if err != nil {
		return "", fmt.Errorf("get user home directory: %w", err)
	}
	return filepath.Join(home, ".kube", "config"), nil
}

func resolveHomeDir(euid int, sudoUser string, lookup userLookupFunc, fallback func() (string, error)) (string, error) {
	if euid == 0 && sudoUser != "" && sudoUser != "root" {
		if u, err := lookup(sudoUser); err == nil && u.HomeDir != "" {
			return u.HomeDir, nil
		}
	}
	return fallback()
}

func loadKubeconfig(kubeconfigPath string) (*rest.Config, error) {
	return loadKubeconfigWithContext(kubeconfigPath, "")
}

func loadKubeconfigWithContext(kubeconfigPath, contextName string) (*rest.Config, error) {
	if kubeconfigPath == "" {
		defaultPath, err := DefaultKubeconfigPath()
		if err != nil {
			return nil, err
		}
		kubeconfigPath = defaultPath
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

func newClientForContext(kubeconfigPath, contextName string) (*rest.Config, kubernetes.Interface, error) {
	config, err := loadKubeconfigWithContext(kubeconfigPath, contextName)
	if err != nil {
		return nil, nil, fmt.Errorf("load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("create kubernetes client: %w", err)
	}

	return config, clientset, nil
}
