package dns

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/byoungmin/kube-service-tunnel/internal/host"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
	proxyadapter "github.com/byoungmin/kube-service-tunnel/internal/proxy"
)

const proxyPort int32 = 80

var errShuttingDown = errors.New("tunnel manager is shutting down")

type DNSManager struct {
	kubeAdapter      kube.KubeAdapterInterface
	hostsFileAdapter host.HostsFileAdapterInterface
	proxyAdapter     proxyadapter.ProxyAdapterInterface

	opMu   sync.Mutex
	closed bool

	mu          sync.RWMutex
	dnsTunnels  []DNSTunnel
	lostHandler func(tunnel DNSTunnel, err error)
}

func NewDNSManager(kubeconfigPath string) (*DNSManager, error) {
	kubeAdapter, err := kube.NewKubeAdapter(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("create kube adapter: %w", err)
	}

	return newDNSManager(kubeAdapter, host.NewHostsFileAdapter(), proxyadapter.NewProxyAdapter()), nil
}

func newDNSManager(
	kubeAdapter kube.KubeAdapterInterface,
	hostsAdapter host.HostsFileAdapterInterface,
	proxyAdapter proxyadapter.ProxyAdapterInterface,
) *DNSManager {
	manager := &DNSManager{
		kubeAdapter:      kubeAdapter,
		hostsFileAdapter: hostsAdapter,
		proxyAdapter:     proxyAdapter,
	}
	kubeAdapter.SetPortForwardLostHandler(manager.handleForwardLost)
	return manager
}

func (m *DNSManager) GetAllDNSTunnels() []DNSTunnel {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]DNSTunnel, len(m.dnsTunnels))
	copy(result, m.dnsTunnels)
	return result
}

func (m *DNSManager) SetTunnelLostHandler(handler func(tunnel DNSTunnel, err error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lostHandler = handler
}

func (m *DNSManager) RegisterAllByContext(ctx context.Context, contextName string, namespaces []string) (BulkRegistrationResult, error) {
	if contextName == "" {
		return BulkRegistrationResult{}, errors.New("context name is required")
	}

	m.opMu.Lock()
	defer m.opMu.Unlock()
	if m.closed {
		return BulkRegistrationResult{}, errShuttingDown
	}

	result, err := m.kubeAdapter.RegisterAllServicesForContext(ctx, contextName, namespaces, m.getUsedPorts(), m.registeredDNS())
	if err != nil {
		return BulkRegistrationResult{Failures: result.Failures}, errors.Join(err, m.stopForwards(result.Tunnels))
	}

	return m.activateAll(result), nil
}

func (m *DNSManager) activateAll(result kube.RegisterAllResult) BulkRegistrationResult {
	if err := m.proxyAdapter.StartIfNotRunning(proxyPort); err != nil {
		failures := append([]kube.ServiceRegistrationFailure(nil), result.Failures...)
		reason := errors.Join(fmt.Errorf("start proxy server: %w", err), m.stopForwards(result.Tunnels)).Error()
		for _, tunnel := range result.Tunnels {
			failures = append(failures, failureOf(tunnel, reason))
		}
		return BulkRegistrationResult{Failures: failures}
	}

	registered := make([]DNSTunnel, 0, len(result.Tunnels))
	failures := append([]kube.ServiceRegistrationFailure(nil), result.Failures...)
	for _, tunnel := range result.Tunnels {
		dnsTunnel := convertToDNSTunnel(tunnel)
		if err := m.activate(dnsTunnel); err != nil {
			failures = append(failures, failureOf(tunnel, err.Error()))
			continue
		}
		registered = append(registered, dnsTunnel)
	}
	return BulkRegistrationResult{Registered: registered, Failures: failures}
}

func (m *DNSManager) RegisterDNSTunnel(ctx context.Context, contextName, serviceName, namespace string) error {
	if contextName == "" || serviceName == "" || namespace == "" {
		return errors.New("context name, service name and namespace are required")
	}

	m.opMu.Lock()
	defer m.opMu.Unlock()
	if m.closed {
		return errShuttingDown
	}

	dnsURL := kube.BuildServiceDNS(serviceName, namespace)
	if owner, found := m.findByDNS(dnsURL); found {
		return fmt.Errorf("%s is already registered (context %s)", dnsURL, owner.Context)
	}

	tunnel, err := m.kubeAdapter.RegisterServicePortForward(ctx, contextName, serviceName, namespace, m.getUsedPorts())
	if err != nil {
		return err
	}

	if err := m.proxyAdapter.StartIfNotRunning(proxyPort); err != nil {
		stopErr := m.stopForwards([]kube.ServiceTunnel{tunnel})
		return errors.Join(fmt.Errorf("start proxy server: %w", err), stopErr)
	}

	return m.activate(convertToDNSTunnel(tunnel))
}

// activate publishes a started forward through the proxy and hosts file, undoing everything on failure.
func (m *DNSManager) activate(tunnel DNSTunnel) error {
	m.proxyAdapter.AddRoute(tunnel.DNSURL, tunnel.LocalPort)
	m.addTunnel(tunnel)

	if err := m.hostsFileAdapter.AddEntry(tunnel.DNSURL); err != nil {
		m.removeTunnel(tunnel.DNSURL)
		m.proxyAdapter.RemoveRoute(tunnel.DNSURL)
		stopErr := m.stopForward(tunnel.ForwardKey)
		return errors.Join(fmt.Errorf("add hosts entry: %w", err), stopErr)
	}
	return nil
}

func (m *DNSManager) UnregisterDNSTunnel(dnsURL string) error {
	if dnsURL == "" {
		return errors.New("DNS URL is required")
	}

	m.opMu.Lock()
	defer m.opMu.Unlock()

	tunnel, found := m.findByDNS(dnsURL)
	if !found {
		return fmt.Errorf("tunnel not found for DNS URL: %s", dnsURL)
	}

	if err := m.hostsFileAdapter.RemoveEntry(dnsURL); err != nil {
		return fmt.Errorf("remove hosts entry: %w", err)
	}
	m.proxyAdapter.RemoveRoute(dnsURL)
	stopErr := m.stopForward(tunnel.ForwardKey)
	m.removeTunnel(dnsURL)

	if stopErr != nil {
		return fmt.Errorf("stop port forward: %w", stopErr)
	}
	return nil
}

func (m *DNSManager) Cleanup() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.closed = true

	m.kubeAdapter.StopAllPortForwards()

	var errs []error
	if err := m.proxyAdapter.Stop(); err != nil {
		errs = append(errs, fmt.Errorf("stop proxy server: %w", err))
	}
	if err := m.hostsFileAdapter.ClearAllEntries(); err != nil {
		errs = append(errs, fmt.Errorf("clear hosts file entries: %w", err))
	}

	m.mu.Lock()
	m.dnsTunnels = nil
	m.mu.Unlock()

	return errors.Join(errs...)
}

func (m *DNSManager) handleForwardLost(forwardKey string, cause error) {
	tunnel, handler, err := m.dropLostTunnel(forwardKey)
	if handler == nil {
		return
	}
	if err != nil {
		cause = fmt.Errorf("%w (cleanup failed: %v)", cause, err)
	}
	handler(tunnel, cause)
}

func (m *DNSManager) dropLostTunnel(forwardKey string) (DNSTunnel, func(DNSTunnel, error), error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	tunnel, found := m.findByForwardKey(forwardKey)
	if !found {
		return DNSTunnel{}, nil, nil
	}

	var err error
	if removeErr := m.hostsFileAdapter.RemoveEntry(tunnel.DNSURL); removeErr != nil {
		err = fmt.Errorf("remove hosts entry: %w", removeErr)
	}
	m.proxyAdapter.RemoveRoute(tunnel.DNSURL)
	m.removeTunnel(tunnel.DNSURL)

	m.mu.RLock()
	handler := m.lostHandler
	m.mu.RUnlock()
	return tunnel, handler, err
}

func (m *DNSManager) stopForward(forwardKey string) error {
	err := m.kubeAdapter.UnregisterServicePortForward(forwardKey)
	if err != nil && !errors.Is(err, kube.ErrPortForwardNotFound) {
		return err
	}
	return nil
}

func (m *DNSManager) stopForwards(tunnels []kube.ServiceTunnel) error {
	var errs []error
	for _, tunnel := range tunnels {
		if err := m.stopForward(tunnel.ForwardKey); err != nil {
			errs = append(errs, fmt.Errorf("stop port forward %s: %w", tunnel.DNSURL, err))
		}
	}
	return errors.Join(errs...)
}

func failureOf(tunnel kube.ServiceTunnel, reason string) kube.ServiceRegistrationFailure {
	return kube.ServiceRegistrationFailure{Namespace: tunnel.Namespace, Service: serviceName(tunnel), Reason: reason}
}

func convertToDNSTunnel(tunnel kube.ServiceTunnel) DNSTunnel {
	return DNSTunnel{
		Context:    tunnel.Context,
		Namespace:  tunnel.Namespace,
		DNSURL:     tunnel.DNSURL,
		Pod:        tunnel.Pod,
		LocalPort:  tunnel.LocalPort,
		RemotePort: tunnel.RemotePort,
		ForwardKey: tunnel.ForwardKey,
	}
}
