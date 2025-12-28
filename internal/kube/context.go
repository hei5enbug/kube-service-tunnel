package kube

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"k8s.io/client-go/tools/clientcmd"
)

type Context struct {
	Name    string
	Cluster string
	User    string
}

type ContextClientInterface interface {
	ListContexts(ctx context.Context) ([]Context, error)
	GetCurrentContext(ctx context.Context) (string, error)
}

type contextClient struct {
	kubeconfigPath string
}

func NewContextClient(kubeconfigPath string) (*contextClient, error) {
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

	return &contextClient{
		kubeconfigPath: kubeconfigPath,
	}, nil
}

func (c *contextClient) ListContexts(ctx context.Context) ([]Context, error) {
	config, err := clientcmd.LoadFromFile(c.kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig file: %w", err)
	}

	var result []Context
	for contextName, context := range config.Contexts {
		result = append(result, Context{
			Name:    contextName,
			Cluster: context.Cluster,
			User:    context.AuthInfo,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result, nil
}

func (c *contextClient) GetCurrentContext(ctx context.Context) (string, error) {
	config, err := clientcmd.LoadFromFile(c.kubeconfigPath)
	if err != nil {
		return "", fmt.Errorf("load kubeconfig file: %w", err)
	}

	return config.CurrentContext, nil
}
