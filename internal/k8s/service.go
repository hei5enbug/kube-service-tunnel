package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Service represents a Kubernetes Service with ClusterIP
type Service struct {
	Name      string
	Namespace string
	ClusterIP string
	Type      string
}

// ServiceLister provides methods to list Kubernetes services
type ServiceLister interface {
	ListServices(ctx context.Context, namespace, contextName string) ([]Service, error)
}

type serviceClient struct {
	kubeconfigPath string
}

func newServiceClient(kubeconfigPath string) (*serviceClient, error) {
	return &serviceClient{
		kubeconfigPath: kubeconfigPath,
	}, nil
}

func (s *serviceClient) ListServices(ctx context.Context, namespace, contextName string) ([]Service, error) {
	config, err := loadKubeconfigWithContext(s.kubeconfigPath, contextName)
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}

	services, err := clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list services in namespace %s: %w", namespace, err)
	}

	var result []Service
	for _, svc := range services.Items {
		// Exclude LoadBalancer and NodePort types
		if svc.Spec.Type == corev1.ServiceTypeLoadBalancer || svc.Spec.Type == corev1.ServiceTypeNodePort {
			continue
		}

		// Include ClusterIP type (or empty type which defaults to ClusterIP) with valid ClusterIP
		// Exclude services without ClusterIP (empty string or "None")
		if svc.Spec.ClusterIP != "" && svc.Spec.ClusterIP != "None" {
			serviceType := string(svc.Spec.Type)
			if serviceType == "" {
				serviceType = string(corev1.ServiceTypeClusterIP)
			}
			result = append(result, Service{
				Name:      svc.Name,
				Namespace: svc.Namespace,
				ClusterIP: svc.Spec.ClusterIP,
				Type:      serviceType,
			})
		}
	}

	return result, nil
}

