package kube

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var (
	errServiceHasNoSelector = errors.New("service has no selector; only pod-backed services can be forwarded")
	errNoReadyPods          = errors.New("no ready pods match service")
)

type PodPort struct {
	Name          string
	ContainerPort int32
	Protocol      string
}

type Pod struct {
	Name        string
	UID         types.UID
	Namespace   string
	Status      string
	Ready       bool
	Terminating bool
	Ports       []PodPort
	Labels      map[string]string
}

type PodInterface interface {
	ListPods(ctx context.Context, namespace, contextName string) ([]Pod, error)
	FindMatchingPods(ctx context.Context, namespace, contextName string, selector map[string]string) ([]Pod, error)
}

type podClient struct {
	kubeconfigPath string
}

func NewPodClient(kubeconfigPath string) (*podClient, error) {
	return &podClient{
		kubeconfigPath: kubeconfigPath,
	}, nil
}

func (p *podClient) ListPods(ctx context.Context, namespace, contextName string) ([]Pod, error) {
	_, clientset, err := newClientForContext(p.kubeconfigPath, contextName)
	if err != nil {
		return nil, err
	}

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pods in namespace %s: %w", namespace, err)
	}

	return podsFromCorev1(pods.Items), nil
}

func (p *podClient) FindMatchingPods(ctx context.Context, namespace, contextName string, selector map[string]string) ([]Pod, error) {
	if len(selector) == 0 {
		return nil, errServiceHasNoSelector
	}

	_, clientset, err := newClientForContext(p.kubeconfigPath, contextName)
	if err != nil {
		return nil, err
	}

	labelSelector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{MatchLabels: selector})
	if err != nil {
		return nil, fmt.Errorf("create label selector: %w", err)
	}

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("list pods with selector in namespace %s: %w", namespace, err)
	}

	return podsFromCorev1(pods.Items), nil
}

func podsFromCorev1(items []corev1.Pod) []Pod {
	var result []Pod
	for _, item := range items {
		result = append(result, podFromCorev1(item))
	}
	return result
}

func podFromCorev1(pod corev1.Pod) Pod {
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

	podLabels := make(map[string]string, len(pod.Labels))
	for k, v := range pod.Labels {
		podLabels[k] = v
	}

	return Pod{
		Name:        pod.Name,
		UID:         pod.UID,
		Namespace:   pod.Namespace,
		Status:      string(pod.Status.Phase),
		Ready:       isPodReady(pod),
		Terminating: pod.DeletionTimestamp != nil,
		Ports:       ports,
		Labels:      podLabels,
	}
}

func isPodReady(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func chooseReadyPod(pods []Pod) (Pod, error) {
	for _, pod := range pods {
		if pod.Status == string(corev1.PodRunning) && pod.Ready && !pod.Terminating {
			return pod, nil
		}
	}
	return Pod{}, errNoReadyPods
}

func resolveTargetPort(httpPort *ServicePort, pod Pod) (int32, error) {
	if httpPort.TargetPort > 0 {
		return httpPort.TargetPort, nil
	}

	if httpPort.TargetPortName == "" {
		return httpPort.Port, nil
	}

	for _, p := range pod.Ports {
		if p.Name == httpPort.TargetPortName {
			return p.ContainerPort, nil
		}
	}
	return 0, fmt.Errorf("pod %s has no container port named %q", pod.Name, httpPort.TargetPortName)
}

func FindPodAndPortForService(
	ctx context.Context,
	podClient PodInterface,
	namespace string,
	contextName string,
	selector map[string]string,
	httpPort *ServicePort,
) (Pod, int32, error) {
	if len(selector) == 0 {
		return Pod{}, 0, errServiceHasNoSelector
	}

	pods, err := podClient.FindMatchingPods(ctx, namespace, contextName, selector)
	if err != nil {
		return Pod{}, 0, fmt.Errorf("find matching pods: %w", err)
	}

	pod, err := chooseReadyPod(pods)
	if err != nil {
		return Pod{}, 0, err
	}

	podPort, err := resolveTargetPort(httpPort, pod)
	if err != nil {
		return Pod{}, 0, err
	}

	return pod, podPort, nil
}
