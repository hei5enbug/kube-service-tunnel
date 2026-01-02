package kube

import (
	"context"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type ServicePort struct {
	Name           string
	Port           int32
	TargetPort     int32
	TargetPortName string
	Protocol       string
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

func NewServiceClient(kubeconfigPath string) (*serviceClient, error) {
	return &serviceClient{
		kubeconfigPath: kubeconfigPath,
	}, nil
}

func (s *serviceClient) ListServices(ctx context.Context, namespace, contextName string) ([]Service, error) {
	_, clientset, err := newClientForContext(s.kubeconfigPath, contextName)
	if err != nil {
		return nil, err
	}

	services, err := clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list services in namespace %s: %w", namespace, err)
	}

	var result []Service
	for _, svc := range services.Items {
		if converted, ok := serviceFromCorev1(svc); ok {
			result = append(result, converted)
		}
	}

	return result, nil
}

func serviceFromCorev1(svc corev1.Service) (Service, bool) {
	if svc.Spec.Type == corev1.ServiceTypeLoadBalancer || svc.Spec.Type == corev1.ServiceTypeNodePort {
		return Service{}, false
	}
	if svc.Spec.ClusterIP == "" || svc.Spec.ClusterIP == corev1.ClusterIPNone {
		return Service{}, false
	}

	serviceType := string(svc.Spec.Type)
	if serviceType == "" {
		serviceType = string(corev1.ServiceTypeClusterIP)
	}

	selector := make(map[string]string, len(svc.Spec.Selector))
	for k, v := range svc.Spec.Selector {
		selector[k] = v
	}

	return Service{
		Name:      svc.Name,
		Namespace: svc.Namespace,
		ClusterIP: svc.Spec.ClusterIP,
		Type:      serviceType,
		Ports:     servicePortsFromCorev1(svc.Spec.Ports),
		Selector:  selector,
	}, true
}

func servicePortsFromCorev1(ports []corev1.ServicePort) []ServicePort {
	var result []ServicePort
	for _, port := range ports {
		converted := ServicePort{
			Name:     port.Name,
			Port:     port.Port,
			Protocol: string(port.Protocol),
		}
		if port.TargetPort.Type == intstr.Int {
			converted.TargetPort = port.TargetPort.IntVal
		} else {
			converted.TargetPortName = port.TargetPort.StrVal
		}
		result = append(result, converted)
	}
	return result
}

var httpPorts = []int32{80, 8080, 3000, 8000, 9000}

var errNoSupportedHTTPPort = errors.New("no supported HTTP port (80, 8080, 3000, 8000, 9000/TCP)")

func PickHTTPPort(svc *Service) *ServicePort {
	for i := range svc.Ports {
		if isTCP(svc.Ports[i].Protocol) && isHTTPPort(svc.Ports[i].Port) {
			return &svc.Ports[i]
		}
	}
	return nil
}

const (
	ReasonNoSelector = "no selector"
	ReasonNoHTTPPort = "no HTTP port"
)

// Eligibility returns the port that would be forwarded and an empty reason when svc can be registered.
// Otherwise it returns a nil port and the reason.
func Eligibility(svc *Service) (*ServicePort, string) {
	if len(svc.Selector) == 0 {
		return nil, ReasonNoSelector
	}
	port := PickHTTPPort(svc)
	if port == nil {
		return nil, ReasonNoHTTPPort
	}
	return port, ""
}

func eligibilityError(reason string) error {
	switch reason {
	case ReasonNoSelector:
		return errServiceHasNoSelector
	case ReasonNoHTTPPort:
		return errNoSupportedHTTPPort
	default:
		return errors.New(reason)
	}
}

func BuildServiceDNS(serviceName, namespace string) string {
	return strings.ToLower(fmt.Sprintf("%s.%s", serviceName, namespace))
}

func isTCP(protocol string) bool {
	return protocol == "" || protocol == string(corev1.ProtocolTCP)
}

func isHTTPPort(port int32) bool {
	for _, p := range httpPorts {
		if port == p {
			return true
		}
	}
	return false
}
