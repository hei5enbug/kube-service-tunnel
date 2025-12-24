package kube

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type NamespaceInterface interface {
	ListNamespaces(ctx context.Context, contextName string) ([]string, error)
	ListNonSystemNamespaces(ctx context.Context, contextName string) ([]string, error)
}

type namespaceClient struct {
	kubeconfigPath string
}

func NewNamespaceClient(kubeconfigPath string) (*namespaceClient, error) {
	return &namespaceClient{
		kubeconfigPath: kubeconfigPath,
	}, nil
}

func (n *namespaceClient) ListNamespaces(ctx context.Context, contextName string) ([]string, error) {
	config, err := loadKubeconfigWithContext(n.kubeconfigPath, contextName)
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}

	namespaces, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}

	var result []string
	for _, ns := range namespaces.Items {
		result = append(result, ns.Name)
	}

	return result, nil
}

func isSystemNamespace(namespace string) bool {
	systemNamespaces := []string{"kube-system", "kube-public", "kube-node-lease"}
	for _, sysNs := range systemNamespaces {
		if namespace == sysNs {
			return true
		}
	}
	return false
}

func (n *namespaceClient) ListNonSystemNamespaces(ctx context.Context, contextName string) ([]string, error) {
	allNamespaces, err := n.ListNamespaces(ctx, contextName)
	if err != nil {
		return nil, err
	}

	var result []string
	for _, ns := range allNamespaces {
		if isSystemNamespace(ns) {
			continue
		}
		result = append(result, ns)
	}
	return result, nil
}
