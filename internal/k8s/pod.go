package k8s

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type PodPort struct {
	Name          string
	ContainerPort int32
	Protocol      string
}

type Pod struct {
	Name      string
	Namespace string
	Status    string
	Ports     []PodPort
	Labels    map[string]string
}

type PodInterface interface {
	ListPods(ctx context.Context, namespace, contextName string) ([]Pod, error)
	FindMatchingPods(ctx context.Context, namespace, contextName string, selector map[string]string) ([]Pod, error)
}

type podClient struct {
	kubeconfigPath string
}

func newPodClient(kubeconfigPath string) (*podClient, error) {
	return &podClient{
		kubeconfigPath: kubeconfigPath,
	}, nil
}

func (p *podClient) ListPods(ctx context.Context, namespace, contextName string) ([]Pod, error) {
	config, err := loadKubeconfigWithContext(p.kubeconfigPath, contextName)
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pods in namespace %s: %w", namespace, err)
	}

	var result []Pod
	for _, pod := range pods.Items {
		var ports []PodPort
		for _, container := range pod.Spec.Containers {
			for _, port := range container.Ports {
				ports = append(ports, PodPort{
					Name:          port.Name,
					ContainerPort: port.ContainerPort,
					Protocol:      string(port.Protocol),
				})
			}
		}

		labels := make(map[string]string)
		if pod.Labels != nil {
			for k, v := range pod.Labels {
				labels[k] = v
			}
		}

		status := string(pod.Status.Phase)
		result = append(result, Pod{
			Name:      pod.Name,
			Namespace: pod.Namespace,
			Status:    status,
			Ports:     ports,
			Labels:    labels,
		})
	}

	return result, nil
}

func (p *podClient) FindMatchingPods(ctx context.Context, namespace, contextName string, selector map[string]string) ([]Pod, error) {
	config, err := loadKubeconfigWithContext(p.kubeconfigPath, contextName)
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}

	labelSelector := metav1.LabelSelector{
		MatchLabels: selector,
	}
	selectorString, err := metav1.LabelSelectorAsSelector(&labelSelector)
	if err != nil {
		return nil, fmt.Errorf("create label selector: %w", err)
	}

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selectorString.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("list pods with selector in namespace %s: %w", namespace, err)
	}

	var result []Pod
	for _, pod := range pods.Items {
		var ports []PodPort
		for _, container := range pod.Spec.Containers {
			for _, port := range container.Ports {
				ports = append(ports, PodPort{
					Name:          port.Name,
					ContainerPort: port.ContainerPort,
					Protocol:      string(port.Protocol),
				})
			}
		}

		labels := make(map[string]string)
		if pod.Labels != nil {
			for k, v := range pod.Labels {
				labels[k] = v
			}
		}

		status := string(pod.Status.Phase)
		result = append(result, Pod{
			Name:      pod.Name,
			Namespace: pod.Namespace,
			Status:    status,
			Ports:     ports,
			Labels:    labels,
		})
	}

	return result, nil
}

