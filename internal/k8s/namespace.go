package k8s

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Namespace provides methods to list Kubernetes namespaces
type Namespace interface {
	ListNamespaces(ctx context.Context, contextName string) ([]string, error)
}

type namespaceClient struct {
	kubeconfigPath string
}

func newNamespaceClient(kubeconfigPath string) (*namespaceClient, error) {
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

