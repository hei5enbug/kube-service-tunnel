package kube

import (
	"context"
	"errors"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	apiCallTimeout   = 30 * time.Second
	discoveryTimeout = 20 * time.Second
)

var ErrNoEligibleServices = errors.New("no eligible services found")

type KubeAdapterInterface interface {
	CurrentContext(ctx context.Context) (string, error)
	FetchAllResources(ctx context.Context) (map[string]ContextResources, error)
	FetchContextResources(ctx context.Context, contextName string) ContextResources
	ListContexts(ctx context.Context) ([]Context, error)
	ListNamespaces(ctx context.Context, contextName string) ([]string, error)
	ListServices(ctx context.Context, namespace, contextName string) ([]Service, error)

	StopAllPortForwards()
	SetPortForwardLostHandler(handler func(forwardKey string, err error))
	RegisterServicesForContext(ctx context.Context, contextName string, targets []ServiceRef, usedPorts map[int32]bool, owners map[string]string, progress func(RegistrationProgress)) (RegisterAllResult, error)
	RegisterServicePortForward(ctx context.Context, contextName, serviceName, namespace string, usedPorts map[int32]bool) (ServiceTunnel, error)
	UnregisterServicePortForward(forwardKey string) error
}

type ServiceTunnel struct {
	Context    string
	Namespace  string
	DNSURL     string
	Pod        string
	LocalPort  int32
	RemotePort int32
	ForwardKey string
}

type ServiceRegistrationFailure struct {
	Namespace string
	Service   string
	Reason    string
}

type ServiceRef struct {
	Namespace string
	Name      string
}

type RegistrationProgress struct {
	Done    int
	Total   int
	Current string
}

type RegisterAllResult struct {
	Tunnels    []ServiceTunnel
	Failures   []ServiceRegistrationFailure
	Skipped    []ServiceRegistrationFailure
	Ineligible []ServiceRegistrationFailure
}

type clientFactory func(contextName string) (*rest.Config, kubernetes.Interface, error)

type kubeAdapter struct {
	kubeconfigPath    string
	contextClient     ContextClientInterface
	namespaceClient   NamespaceInterface
	podClient         PodInterface
	serviceClient     ServiceInterface
	portForwardClient PortForwardClientInterface
	newClient         clientFactory
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

	return &kubeAdapter{
		kubeconfigPath:    kubeconfigPath,
		contextClient:     ctxClient,
		namespaceClient:   nsClient,
		podClient:         pClient,
		serviceClient:     svcClient,
		portForwardClient: NewPortForwardClient(),
		newClient: func(contextName string) (*rest.Config, kubernetes.Interface, error) {
			return newClientForContext(kubeconfigPath, contextName)
		},
	}, nil
}

func (m *kubeAdapter) CurrentContext(ctx context.Context) (string, error) {
	return m.contextClient.GetCurrentContext(ctx)
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

func (m *kubeAdapter) SetPortForwardLostHandler(handler func(forwardKey string, err error)) {
	m.portForwardClient.SetLostHandler(handler)
}

func (m *kubeAdapter) UnregisterServicePortForward(forwardKey string) error {
	return m.portForwardClient.StopPortForward(forwardKey)
}
