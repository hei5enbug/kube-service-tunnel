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

// RegisterServices registers the given services of one context in bulk. progress, when non-nil, is called
// synchronously while the manager operation lock is held, so it must not call back into the manager.
// Targets with an invalid hostname or local port become failures, and targets whose service already has a
// tunnel become skipped; neither reaches the kube adapter.
func (m *DNSManager) RegisterServices(ctx context.Context, contextName string, targets []kube.ServiceRef, progress func(kube.RegistrationProgress)) (BulkRegistrationResult, error) {
	if contextName == "" {
		return BulkRegistrationResult{}, errors.New("context name is required")
	}

	m.opMu.Lock()
	defer m.opMu.Unlock()
	if m.closed {
		return BulkRegistrationResult{}, errShuttingDown
	}

	pending, rejected := m.screenTargets(contextName, targets)
	if len(targets) > 0 && len(pending) == 0 {
		return rejected, nil
	}

	result, err := m.kubeAdapter.RegisterServicesForContext(ctx, contextName, pending, m.getUsedPorts(), m.registeredOwners(), progress)
	if err != nil {
		return rejected.merge(BulkRegistrationResult{
			Failures:   result.Failures,
			Skipped:    result.Skipped,
			Ineligible: result.Ineligible,
		}), errors.Join(err, m.stopForwards(result.Tunnels))
	}

	return rejected.merge(m.activateAll(result)), nil
}

// screenTargets splits targets into those the kube adapter may register and a result holding the rest.
func (m *DNSManager) screenTargets(contextName string, targets []kube.ServiceRef) ([]kube.ServiceRef, BulkRegistrationResult) {
	var rejected BulkRegistrationResult
	pending := make([]kube.ServiceRef, 0, len(targets))
	for _, target := range targets {
		normalized, err := normalizeTarget(target)
		if err != nil {
			rejected.Failures = append(rejected.Failures, kube.ServiceRegistrationFailure{Namespace: target.Namespace, Service: target.Name, Reason: err.Error()})
			continue
		}
		if existing, found := m.findByIdentity(contextName, target.Namespace, target.Name); found {
			reason := "already registered as http://" + existing.DNSURL
			rejected.Skipped = append(rejected.Skipped, kube.ServiceRegistrationFailure{Namespace: target.Namespace, Service: target.Name, Reason: reason})
			continue
		}
		pending = append(pending, normalized)
	}
	return pending, rejected
}

func (r BulkRegistrationResult) merge(later BulkRegistrationResult) BulkRegistrationResult {
	return BulkRegistrationResult{
		Registered: later.Registered,
		Failures:   append(append([]kube.ServiceRegistrationFailure(nil), r.Failures...), later.Failures...),
		Skipped:    append(append([]kube.ServiceRegistrationFailure(nil), r.Skipped...), later.Skipped...),
		Ineligible: later.Ineligible,
	}
}

func (m *DNSManager) activateAll(result kube.RegisterAllResult) BulkRegistrationResult {
	if err := m.proxyAdapter.StartIfNotRunning(proxyPort); err != nil {
		failures := append([]kube.ServiceRegistrationFailure(nil), result.Failures...)
		reason := errors.Join(fmt.Errorf("start proxy server: %w", err), m.stopForwards(result.Tunnels)).Error()
		for _, tunnel := range result.Tunnels {
			failures = append(failures, failureOf(tunnel, reason))
		}
		return BulkRegistrationResult{Failures: failures, Skipped: result.Skipped, Ineligible: result.Ineligible}
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
	return BulkRegistrationResult{Registered: registered, Failures: failures, Skipped: result.Skipped, Ineligible: result.Ineligible}
}

// RegisterDNSTunnel opens a tunnel for one service and returns it once it is active. A zero
// target.Hostname or target.LocalPort selects the defaults; a busy preferred port falls back to an
// automatic one (see DNSTunnel.RequestedLocalPort).
func (m *DNSManager) RegisterDNSTunnel(ctx context.Context, contextName string, target kube.ServiceRef) (DNSTunnel, error) {
	if contextName == "" || target.Name == "" || target.Namespace == "" {
		return DNSTunnel{}, errors.New("context name, service name and namespace are required")
	}
	target, err := normalizeTarget(target)
	if err != nil {
		return DNSTunnel{}, err
	}

	m.opMu.Lock()
	defer m.opMu.Unlock()
	if m.closed {
		return DNSTunnel{}, errShuttingDown
	}

	if existing, found := m.findByIdentity(contextName, target.Namespace, target.Name); found {
		return DNSTunnel{}, fmt.Errorf("%s/%s is already registered as http://%s", target.Namespace, target.Name, existing.DNSURL)
	}
	dnsURL := target.EffectiveHostname()
	if owner, found := m.findByDNS(dnsURL); found {
		return DNSTunnel{}, fmt.Errorf("%s is already registered (context %s)", dnsURL, owner.Context)
	}

	tunnel, err := m.kubeAdapter.RegisterServicePortForward(ctx, contextName, target, m.getUsedPorts())
	if err != nil {
		return DNSTunnel{}, err
	}

	if err := m.proxyAdapter.StartIfNotRunning(proxyPort); err != nil {
		stopErr := m.stopForwards([]kube.ServiceTunnel{tunnel})
		return DNSTunnel{}, errors.Join(fmt.Errorf("start proxy server: %w", err), stopErr)
	}

	dnsTunnel := convertToDNSTunnel(tunnel)
	if err := m.activate(dnsTunnel); err != nil {
		return DNSTunnel{}, err
	}
	return dnsTunnel, nil
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
	return kube.ServiceRegistrationFailure{Namespace: tunnel.Namespace, Service: tunnel.Service, Reason: reason}
}

func convertToDNSTunnel(tunnel kube.ServiceTunnel) DNSTunnel {
	return DNSTunnel{
		Context:            tunnel.Context,
		Namespace:          tunnel.Namespace,
		Service:            tunnel.Service,
		DNSURL:             tunnel.DNSURL,
		Pod:                tunnel.Pod,
		LocalPort:          tunnel.LocalPort,
		RequestedLocalPort: tunnel.RequestedLocalPort,
		RemotePort:         tunnel.RemotePort,
		ForwardKey:         tunnel.ForwardKey,
	}
}
