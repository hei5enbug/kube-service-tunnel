package dns

import (
	"context"
	"fmt"
	"sync"

	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

type fakeKube struct {
	mu            sync.Mutex
	registerCalls int
	registerFn    func(contextName string, target kube.ServiceRef) (kube.ServiceTunnel, error)
	registerSeen  []kube.ServiceRef
	startFn       func(pod string, remotePort, localPort int32) (string, error)
	starts        []startedForward
	bulkResult    kube.RegisterAllResult
	bulkErr       error
	bulkOwners    map[string]string
	bulkTargets   []kube.ServiceRef
	bulkContext   string
	bulkProgress  []kube.RegistrationProgress
	stopped       []string
	stopErr       error
	stopAllCalls  int
	lostHandler   func(forwardKey string, err error)
}

func (f *fakeKube) CurrentContext(ctx context.Context) (string, error) { return "", nil }

func (f *fakeKube) FetchAllResources(ctx context.Context) (map[string]kube.ContextResources, error) {
	return nil, nil
}

func (f *fakeKube) FetchContextResources(ctx context.Context, contextName string) kube.ContextResources {
	return kube.ContextResources{}
}

func (f *fakeKube) ListContexts(ctx context.Context) ([]kube.Context, error) { return nil, nil }

func (f *fakeKube) ListNamespaces(ctx context.Context, contextName string) ([]string, error) {
	return nil, nil
}

func (f *fakeKube) ListServices(ctx context.Context, namespace, contextName string) ([]kube.Service, error) {
	return nil, nil
}

func (f *fakeKube) StopAllPortForwards() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopAllCalls++
}

func (f *fakeKube) SetPortForwardLostHandler(handler func(forwardKey string, err error)) {
	f.lostHandler = handler
}

func (f *fakeKube) RegisterServicesForContext(
	ctx context.Context, contextName string, targets []kube.ServiceRef, usedPorts map[int32]bool, owners map[string]string, progress func(kube.RegistrationProgress),
) (kube.RegisterAllResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bulkContext = contextName
	f.bulkTargets = targets
	f.bulkOwners = owners
	for _, p := range f.bulkProgress {
		if progress != nil {
			progress(p)
		}
	}
	return f.bulkResult, f.bulkErr
}

func (f *fakeKube) RegisterServicePortForward(
	ctx context.Context, contextName string, target kube.ServiceRef, usedPorts map[int32]bool,
) (kube.ServiceTunnel, error) {
	f.mu.Lock()
	f.registerCalls++
	f.registerSeen = append(f.registerSeen, target)
	fn := f.registerFn
	f.mu.Unlock()
	if fn == nil {
		return kube.ServiceTunnel{}, fmt.Errorf("unexpected register call")
	}
	return fn(contextName, target)
}

type startedForward struct {
	contextName string
	namespace   string
	pod         string
	remotePort  int32
	localPort   int32
}

func (f *fakeKube) StartForward(ctx context.Context, contextName, namespace, pod string, remotePort, localPort int32) (string, error) {
	f.mu.Lock()
	fn := f.startFn
	f.starts = append(f.starts, startedForward{contextName, namespace, pod, remotePort, localPort})
	f.mu.Unlock()
	if fn != nil {
		return fn(pod, remotePort, localPort)
	}
	return fmt.Sprintf("fwd-%s-%d", pod, localPort), nil
}

func (f *fakeKube) startedForwards() []startedForward {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]startedForward(nil), f.starts...)
}

func (f *fakeKube) UnregisterServicePortForward(forwardKey string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, forwardKey)
	return f.stopErr
}

func (f *fakeKube) stoppedKeys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.stopped...)
}

type fakeHosts struct {
	mu         sync.Mutex
	entries    map[string]bool
	addErrs    map[string]error
	removeErr  error
	clearErr   error
	clearCalls int
}

func newFakeHosts() *fakeHosts {
	return &fakeHosts{entries: map[string]bool{}, addErrs: map[string]error{}}
}

func (h *fakeHosts) AddEntry(dnsURL string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.addErrs[dnsURL]; err != nil {
		return err
	}
	h.entries[dnsURL] = true
	return nil
}

func (h *fakeHosts) RemoveEntry(dnsURL string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.removeErr != nil {
		return h.removeErr
	}
	delete(h.entries, dnsURL)
	return nil
}

func (h *fakeHosts) ClearAllEntries() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clearCalls++
	h.entries = map[string]bool{}
	return h.clearErr
}

func (h *fakeHosts) has(dnsURL string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.entries[dnsURL]
}

type fakeProxy struct {
	mu        sync.Mutex
	startErr  error
	stopErr   error
	stopCalls int
	routes    map[string]int32
}

func newFakeProxy() *fakeProxy {
	return &fakeProxy{routes: map[string]int32{}}
}

func (p *fakeProxy) StartIfNotRunning(port int32) error { return p.startErr }

func (p *fakeProxy) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopCalls++
	return p.stopErr
}

func (p *fakeProxy) AddRoute(host string, localPort int32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.routes[host] = localPort
}

func (p *fakeProxy) AddRoutes(routes map[string]int32) {
	for host, port := range routes {
		p.AddRoute(host, port)
	}
}

func (p *fakeProxy) RemoveRoute(host string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.routes, host)
}

func (p *fakeProxy) hasRoute(host string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.routes[host]
	return ok
}

func tunnelFor(contextName, serviceName, namespace string, localPort int32) kube.ServiceTunnel {
	return tunnelForTarget(contextName, kube.ServiceRef{Namespace: namespace, Name: serviceName}, localPort)
}

func tunnelForTarget(contextName string, target kube.ServiceRef, localPort int32) kube.ServiceTunnel {
	dnsURL := target.EffectiveHostname()
	return kube.ServiceTunnel{
		Context:            contextName,
		Namespace:          target.Namespace,
		Service:            target.Name,
		DNSURL:             dnsURL,
		Pod:                target.Name + "-pod",
		LocalPort:          localPort,
		RequestedLocalPort: target.LocalPort,
		RemotePort:         80,
		ForwardKey:         "key-" + dnsURL,
	}
}

func newTestManager() (*DNSManager, *fakeKube, *fakeHosts, *fakeProxy) {
	k, h, p := &fakeKube{}, newFakeHosts(), newFakeProxy()
	k.registerFn = func(contextName string, target kube.ServiceRef) (kube.ServiceTunnel, error) {
		port := target.LocalPort
		if port == 0 {
			port = 40000
		}
		return tunnelForTarget(contextName, target, port), nil
	}
	return newDNSManager(k, h, p), k, h, p
}

func (p *fakeProxy) routePort(host string) int32 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.routes[host]
}
