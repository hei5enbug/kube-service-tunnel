package kube

import (
	"context"
	"fmt"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type KubeAdapterInterface interface {
	ListContexts(ctx context.Context) ([]Context, error)

	ListNamespaces(ctx context.Context, contextName string) ([]string, error)

	ListServices(ctx context.Context, namespace, contextName string) ([]Service, error)

	StopAllPortForwards()

	RegisterAllServicesForContext(contextName string, usedPorts map[int32]bool) ([]ServiceTunnel, error)
	RegisterServicePortForward(contextName, serviceName, namespace string, usedPorts map[int32]bool) (ServiceTunnel, error)
	UnregisterServicePortForward(contextName, namespace, pod string, remotePort int32) error
}

type ServiceTunnel struct {
	Context    string
	Namespace  string
	DNSURL     string
	Pod        string
	LocalPort  int32
	RemotePort int32
}

type kubeAdapter struct {
	kubeconfigPath    string
	contextClient     ContextClientInterface
	namespaceClient   NamespaceInterface
	podClient         PodInterface
	serviceClient     ServiceInterface
	portForwardClient PortForwardClientInterface
}

func NewKubeAdapter(kubeconfigPath string) (KubeAdapterInterface, error) {
	ctxClient, err := NewContextClient(kubeconfigPath)
	if err != nil {
		return nil, err
	}

	nsClient, err := NewNamespaceClient(kubeconfigPath)
	if err != nil {
		return nil, err
	}

	pClient, err := NewPodClient(kubeconfigPath)
	if err != nil {
		return nil, err
	}

	svcClient, err := NewServiceClient(kubeconfigPath)
	if err != nil {
		return nil, err
	}

	pfClient := NewPortForwardClient()

	return &kubeAdapter{
		kubeconfigPath:    kubeconfigPath,
		contextClient:     ctxClient,
		namespaceClient:   nsClient,
		podClient:         pClient,
		serviceClient:     svcClient,
		portForwardClient: pfClient,
	}, nil
}

func (m *kubeAdapter) ListContexts(ctx context.Context) ([]Context, error) {
	return m.contextClient.ListContexts(ctx)
}

func (m *kubeAdapter) ListNamespaces(ctx context.Context, contextName string) ([]string, error) {
	return m.namespaceClient.ListNamespaces(ctx, contextName)
}

func (m *kubeAdapter) ListServices(ctx context.Context, namespace, contextName string) ([]Service, error) {
	return m.serviceClient.ListServices(ctx, namespace, contextName)
}

func (m *kubeAdapter) StopAllPortForwards() {
	m.portForwardClient.StopAllPortForwards()
}

func (m *kubeAdapter) RegisterAllServicesForContext(contextName string, usedPorts map[int32]bool) ([]ServiceTunnel, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	namespaces, err := m.namespaceClient.ListNonSystemNamespaces(ctx, contextName)
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}

	config, err := loadKubeconfigWithContext(m.kubeconfigPath, contextName)
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}

	var tunnels []ServiceTunnel
	for _, ns := range namespaces {
		nsTunnels, err := m.registerNamespaceServices(ctx, contextName, ns, usedPorts, config, clientset)
		if err != nil {
			continue
		}
		tunnels = append(tunnels, nsTunnels...)
	}

	if len(tunnels) == 0 {
		return nil, fmt.Errorf("no services found to register")
	}

	return tunnels, nil
}

func (m *kubeAdapter) registerNamespaceServices(
	ctx context.Context,
	contextName string,
	namespace string,
	usedPorts map[int32]bool,
	config *rest.Config,
	clientset kubernetes.Interface,
) ([]ServiceTunnel, error) {
	services, err := m.ListServices(ctx, namespace, contextName)
	if err != nil {
		return nil, err
	}

	var tunnels []ServiceTunnel

	for _, svc := range services {
		if svc.ClusterIP == "" {
			continue
		}

		httpPort := PickHTTPPort(&svc)
		if httpPort == nil {
			continue
		}

		pod, podPort, err := FindPodAndPortForService(ctx, m.podClient, namespace, contextName, svc.Selector, httpPort)
		if err != nil {
			continue
		}

		localPort, err := findAvailablePort(40000, usedPorts)
		if err != nil {
			continue
		}

		if err := m.portForwardClient.StartPortForward(contextName, namespace, pod.Name, localPort, podPort, config, clientset); err != nil {
			continue
		}

		serviceDNS := BuildServiceDNS(svc.Name, namespace, httpPort.Port)

		tunnels = append(tunnels, ServiceTunnel{
			Context:    contextName,
			Namespace:  namespace,
			DNSURL:     serviceDNS,
			Pod:        pod.Name,
			LocalPort:  localPort,
			RemotePort: podPort,
		})

		usedPorts[localPort] = true
	}

	return tunnels, nil
}

func (m *kubeAdapter) RegisterServicePortForward(contextName, serviceName, namespace string, usedPorts map[int32]bool) (ServiceTunnel, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	services, err := m.ListServices(ctx, namespace, contextName)
	if err != nil {
		return ServiceTunnel{}, fmt.Errorf("list services: %w", err)
	}

	var targetService *Service
	for _, svc := range services {
		if svc.Name == serviceName && svc.Namespace == namespace {
			if svc.ClusterIP == "" {
				return ServiceTunnel{}, fmt.Errorf("service %s/%s has no ClusterIP", namespace, serviceName)
			}
			targetService = &svc
			break
		}
	}

	if targetService == nil {
		return ServiceTunnel{}, fmt.Errorf("service %s/%s not found in current namespace", namespace, serviceName)
	}

	httpPort := PickHTTPPort(targetService)
	if httpPort == nil {
		return ServiceTunnel{}, fmt.Errorf("no HTTP port found for service %s/%s", namespace, serviceName)
	}

	pod, podPort, err := FindPodAndPortForService(ctx, m.podClient, namespace, contextName, targetService.Selector, httpPort)
	if err != nil {
		return ServiceTunnel{}, fmt.Errorf("find matching pods: %w", err)
	}

	localPort, err := findAvailablePort(40000, usedPorts)
	if err != nil {
		return ServiceTunnel{}, fmt.Errorf("find available port: %w", err)
	}

	config, err := loadKubeconfigWithContext(m.kubeconfigPath, contextName)
	if err != nil {
		return ServiceTunnel{}, fmt.Errorf("load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return ServiceTunnel{}, fmt.Errorf("create kubernetes client: %w", err)
	}

	err = m.portForwardClient.StartPortForward(contextName, namespace, pod.Name, localPort, podPort, config, clientset)
	if err != nil {
		return ServiceTunnel{}, fmt.Errorf("start port forward: %w", err)
	}

	serviceDNS := BuildServiceDNS(serviceName, namespace, httpPort.Port)

	return ServiceTunnel{
		Context:    contextName,
		Namespace:  namespace,
		DNSURL:     serviceDNS,
		Pod:        pod.Name,
		LocalPort:  localPort,
		RemotePort: podPort,
	}, nil
}

func (m *kubeAdapter) UnregisterServicePortForward(contextName, namespace, pod string, remotePort int32) error {
	key := BuildPortForwardKey(contextName, namespace, pod, remotePort)
	return m.portForwardClient.StopPortForward(key)
}
