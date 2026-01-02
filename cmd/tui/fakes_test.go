package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

type registerCall struct {
	Context   string
	Service   string
	Namespace string
}

type bulkCall struct {
	Context string
	Targets []kube.ServiceRef
}

type fakeManager struct {
	mu          sync.Mutex
	tunnels     []dns.DNSTunnel
	registers   []registerCall
	bulks       []bulkCall
	unregisters []string
	lost        func(dns.DNSTunnel, error)
	registerErr error
	bulkGate    chan struct{}
	bulkResult  dns.BulkRegistrationResult
	bulkErr     error
}

func (m *fakeManager) GetAllDNSTunnels() []dns.DNSTunnel {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.tunnels)
}

func (m *fakeManager) RegisterServices(ctx context.Context, contextName string, targets []kube.ServiceRef, progress func(kube.RegistrationProgress)) (dns.BulkRegistrationResult, error) {
	m.mu.Lock()
	m.bulks = append(m.bulks, bulkCall{Context: contextName, Targets: slices.Clone(targets)})
	gate, result, err := m.bulkGate, m.bulkResult, m.bulkErr
	m.mu.Unlock()

	for i, target := range targets {
		progress(kube.RegistrationProgress{Done: i, Total: len(targets), Current: kube.BuildServiceDNS(target.Name, target.Namespace)})
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
		}
	}
	progress(kube.RegistrationProgress{Done: len(targets), Total: len(targets)})
	return result, err
}

func (m *fakeManager) RegisterDNSTunnel(_ context.Context, contextName, serviceName, namespace string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registers = append(m.registers, registerCall{Context: contextName, Service: serviceName, Namespace: namespace})
	if m.registerErr != nil {
		return m.registerErr
	}
	m.tunnels = append(m.tunnels, tunnel(contextName, namespace, serviceName))
	return nil
}

func (m *fakeManager) UnregisterDNSTunnel(dnsURL string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unregisters = append(m.unregisters, dnsURL)
	m.tunnels = slices.DeleteFunc(slices.Clone(m.tunnels), func(t dns.DNSTunnel) bool { return t.DNSURL == dnsURL })
	return nil
}

func (m *fakeManager) SetTunnelLostHandler(handler func(dns.DNSTunnel, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lost = handler
}

func (m *fakeManager) Cleanup() error { return nil }

func (m *fakeManager) loseTunnel(dnsURL string) {
	m.mu.Lock()
	index := slices.IndexFunc(m.tunnels, func(t dns.DNSTunnel) bool { return t.DNSURL == dnsURL })
	lostTunnel := m.tunnels[index]
	m.tunnels = slices.Delete(slices.Clone(m.tunnels), index, index+1)
	handler := m.lost
	m.mu.Unlock()
	handler(lostTunnel, errors.New("pod restarted"))
}

func (m *fakeManager) unregistered() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.unregisters)
}

func (m *fakeManager) bulkCalls() []bulkCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.bulks)
}

func (m *fakeManager) registerCalls() []registerCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.registers)
}

func tunnel(contextName, namespace, serviceName string) dns.DNSTunnel {
	dnsURL := kube.BuildServiceDNS(serviceName, namespace)
	return dns.DNSTunnel{
		Context:    contextName,
		Namespace:  namespace,
		DNSURL:     dnsURL,
		Pod:        serviceName + "-7d9f8b6c5-x2k4q",
		LocalPort:  40000,
		RemotePort: 8080,
		ForwardKey: contextName + "/" + dnsURL,
	}
}

type fakeKube struct {
	mu           sync.Mutex
	current      string
	currentErr   error
	resources    map[string]kube.ContextResources
	listErr      error
	refreshed    map[string]kube.ContextResources
	refreshCalls []string
}

func (k *fakeKube) CurrentContext(context.Context) (string, error) {
	return k.current, k.currentErr
}

func (k *fakeKube) FetchAllResources(context.Context) (map[string]kube.ContextResources, error) {
	return k.resources, k.listErr
}

func (k *fakeKube) FetchContextResources(_ context.Context, contextName string) kube.ContextResources {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.refreshCalls = append(k.refreshCalls, contextName)
	return k.refreshed[contextName]
}

func (k *fakeKube) ListContexts(context.Context) ([]kube.Context, error) { return nil, nil }

func (k *fakeKube) ListNamespaces(context.Context, string) ([]string, error) { return nil, nil }

func (k *fakeKube) ListServices(context.Context, string, string) ([]kube.Service, error) {
	return nil, nil
}

func (k *fakeKube) StopAllPortForwards() {}

func (k *fakeKube) SetPortForwardLostHandler(func(string, error)) {}

func (k *fakeKube) RegisterServicesForContext(context.Context, string, []kube.ServiceRef, map[int32]bool, map[string]string, func(kube.RegistrationProgress)) (kube.RegisterAllResult, error) {
	return kube.RegisterAllResult{}, nil
}

func (k *fakeKube) RegisterServicePortForward(context.Context, string, string, string, map[int32]bool) (kube.ServiceTunnel, error) {
	return kube.ServiceTunnel{}, nil
}

func (k *fakeKube) UnregisterServicePortForward(string) error { return nil }

type fakeCopier struct {
	mu    sync.Mutex
	texts []string
	err   error
}

func (c *fakeCopier) Copy(_ context.Context, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.texts = append(c.texts, text)
	return c.err
}

func (c *fakeCopier) copied() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.texts)
}

func httpService(namespace, name string, port int32) kube.Service {
	return kube.Service{
		Name:      name,
		Namespace: namespace,
		ClusterIP: "10.0.0.10",
		Type:      "ClusterIP",
		Ports:     []kube.ServicePort{{Name: "http", Port: port, TargetPort: port, Protocol: "TCP"}},
		Selector:  map[string]string{"app": name},
	}
}

func noSelectorService(namespace, name string) kube.Service {
	svc := httpService(namespace, name, 80)
	svc.Selector = nil
	return svc
}

func grpcService(namespace, name string) kube.Service {
	svc := httpService(namespace, name, 50051)
	svc.Ports[0].Name = "grpc"
	return svc
}

// sampleCluster has dev (api, default) and prod (shop) with one failed discovery in prod.
func sampleCluster() map[string]kube.ContextResources {
	return map[string]kube.ContextResources{
		"dev": {Namespaces: map[string][]kube.Service{
			"api": {
				httpService("api", "admin", 3000),
				grpcService("api", "grpc"),
				noSelectorService("api", "ext"),
				httpService("api", "web", 8080),
				httpService("api", "website", 80),
			},
			"default": {httpService("default", "hello", 80)},
		}},
		"prod": {
			Namespaces: map[string][]kube.Service{"shop": {httpService("shop", "cart", 80), httpService("shop", "pay", 8000)}},
			Err:        fmt.Errorf("context prod namespace audit: %w", errors.New("forbidden")),
		},
	}
}
