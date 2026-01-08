package kube

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
	RegisterServicePortForward(ctx context.Context, contextName string, target ServiceRef, usedPorts map[int32]bool) (ServiceTunnel, error)
	StartForward(ctx context.Context, contextName, namespace, pod string, remotePort, localPort int32) (string, error)
	UnregisterServicePortForward(forwardKey string) error
}

type ServiceTunnel struct {
	Context            string
	Namespace          string
	Service            string
	DNSURL             string
	Pod                string
	LocalPort          int32
	RequestedLocalPort int32
	RemotePort         int32
	ForwardKey         string
}

type ServiceRegistrationFailure struct {
	Namespace string
	Service   string
	Reason    string
}

// ServiceRef identifies a service to tunnel. A zero Hostname or LocalPort selects the default hostname
// (service.namespace) or an automatically chosen local port.
type ServiceRef struct {
	Namespace string
	Name      string
	Hostname  string
	LocalPort int32
}

// EffectiveHostname returns the lowercased custom hostname, or the default service DNS name.
func (r ServiceRef) EffectiveHostname() string {
	if r.Hostname != "" {
		return strings.ToLower(r.Hostname)
	}
	return BuildServiceDNS(r.Name, r.Namespace)
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

// StartForward opens a port forward from 127.0.0.1:localPort to the pod's remotePort and returns its key.
func (m *kubeAdapter) StartForward(ctx context.Context, contextName, namespace, pod string, remotePort, localPort int32) (string, error) {
	config, clientset, err := m.newClient(contextName)
	if err != nil {
		return "", err
	}
	if clientset != nil {
		uid, err := readPodUID(ctx, clientset, namespace, pod)
		if err != nil {
			return "", err
		}
		ctx = withExpectedPodUID(ctx, uid)
	}
	return m.portForwardClient.StartPortForward(ctx, contextName, namespace, pod, localPort, remotePort, config, clientset)
}

// readPodUID reads the pod once so the forward can be tied to exactly this pod instance.
func readPodUID(ctx context.Context, clientset kubernetes.Interface, namespace, pod string) (types.UID, error) {
	readCtx, cancel := withAPITimeout(ctx)
	defer cancel()
	current, err := clientset.CoreV1().Pods(namespace).Get(readCtx, pod, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("read pod %s/%s: %w", namespace, pod, err)
	}
	return current.UID, nil
}
