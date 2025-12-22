package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

type ServicePort struct {
	Name       string
	Port       int32
	TargetPort int32
	Protocol   string
}

type Service struct {
	Name      string
	Namespace string
	ClusterIP string
	Type      string
	Ports     []ServicePort
	Selector  map[string]string
}

type ServiceInterface interface {
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
		if svc.Spec.Type == corev1.ServiceTypeLoadBalancer || svc.Spec.Type == corev1.ServiceTypeNodePort {
			continue
		}

		if svc.Spec.ClusterIP != "" && svc.Spec.ClusterIP != "None" {
			serviceType := string(svc.Spec.Type)
			if serviceType == "" {
				serviceType = string(corev1.ServiceTypeClusterIP)
			}

			var ports []ServicePort
			for _, port := range svc.Spec.Ports {
				targetPort := int32(0)
				if port.TargetPort.Type == intstr.Int {
					targetPort = port.TargetPort.IntVal
				}
				ports = append(ports, ServicePort{
					Name:       port.Name,
					Port:       port.Port,
					TargetPort: targetPort,
					Protocol:   string(port.Protocol),
				})
			}

			selector := make(map[string]string)
			if svc.Spec.Selector != nil {
				for k, v := range svc.Spec.Selector {
					selector[k] = v
				}
			}

			result = append(result, Service{
				Name:      svc.Name,
				Namespace: svc.Namespace,
				ClusterIP: svc.Spec.ClusterIP,
				Type:      serviceType,
				Ports:     ports,
				Selector:  selector,
			})
		}
	}

	return result, nil
}

