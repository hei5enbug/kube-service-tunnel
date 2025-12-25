package dns

import (
	"fmt"
	"strings"
	"sync"

	"github.com/byoungmin/kube-service-tunnel/internal/host"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
	proxyadapter "github.com/byoungmin/kube-service-tunnel/internal/proxy"
)

type DNSTunnel struct {
	Context    string
	Namespace  string
	DNSURL     string
	Pod        string
	LocalPort  int32
	RemotePort int32
}

type DNSManager struct {
	kubeconfigPath   string
	kubeAdapter      kube.KubeAdapterInterface
	hostsFileAdapter host.HostsFileAdapterInterface
	proxyAdapter     proxyadapter.ProxyAdapterInterface
	dnsTunnels       []DNSTunnel
	mu               sync.RWMutex
}

func NewDNSManager(kubeconfigPath string) (*DNSManager, error) {
	kubeAdapter, err := kube.NewKubeAdapter(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("create kube adapter: %w", err)
	}

	dnsManager := &DNSManager{
		kubeconfigPath:   kubeconfigPath,
		kubeAdapter:      kubeAdapter,
		hostsFileAdapter: host.NewHostsFileAdapter(),
		proxyAdapter:     proxyadapter.NewProxyAdapter(),
	}

	return dnsManager, nil
}

func (m *DNSManager) GetAllDNSTunnels() []DNSTunnel {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]DNSTunnel, len(m.dnsTunnels))
	copy(result, m.dnsTunnels)
	return result
}

func (m *DNSManager) RegisterAllByContext(contextName string) error {
	if contextName == "" {
		return fmt.Errorf("context name is required")
	}

	usedPorts := m.getUsedPorts()

	tunnels, err := m.kubeAdapter.RegisterAllServicesForContext(contextName, usedPorts)
	if err != nil {
		return err
	}

	if err := m.proxyAdapter.StartIfNotRunning(80); err != nil {
		return fmt.Errorf("start proxy server: %w", err)
	}

	routes := make(map[string]int32, len(tunnels))
	dnsTunnels := make([]DNSTunnel, 0, len(tunnels))
	for _, tunnel := range tunnels {
		dnsTunnels = append(dnsTunnels, convertToDNSTunnel(tunnel))
		routes[tunnel.DNSURL] = tunnel.LocalPort
	}

	m.addTunnels(dnsTunnels)
	m.proxyAdapter.AddRoutes(routes)

	for i, tunnel := range tunnels {
		if err := m.hostsFileAdapter.AddEntry(tunnel.DNSURL); err != nil {
			for j := 0; j < i; j++ {
				m.hostsFileAdapter.RemoveEntry(tunnels[j].DNSURL)
			}
			for _, dnsTunnel := range dnsTunnels {
				m.removeTunnel(dnsTunnel.DNSURL)
				m.proxyAdapter.RemoveRoute(dnsTunnel.DNSURL)
				m.kubeAdapter.UnregisterServicePortForward(dnsTunnel.Context, dnsTunnel.Namespace, dnsTunnel.Pod, dnsTunnel.RemotePort)
			}
			return fmt.Errorf("add hosts entry: %w", err)
		}
	}

	return nil
}

func (m *DNSManager) RegisterDNSTunnel(contextName, serviceName, namespace string) error {
	if contextName == "" || serviceName == "" || namespace == "" {
		return fmt.Errorf("context name, service name and namespace are required")
	}

	usedPorts := m.getUsedPorts()

	tunnel, err := m.kubeAdapter.RegisterServicePortForward(contextName, serviceName, namespace, usedPorts)
	if err != nil {
		return err
	}

	if err := m.proxyAdapter.StartIfNotRunning(80); err != nil {
		m.kubeAdapter.UnregisterServicePortForward(tunnel.Context, tunnel.Namespace, tunnel.Pod, tunnel.RemotePort)
		return fmt.Errorf("start proxy server: %w", err)
	}

	m.proxyAdapter.AddRoute(tunnel.DNSURL, tunnel.LocalPort)

	m.addTunnels([]DNSTunnel{convertToDNSTunnel(tunnel)})

	if err := m.hostsFileAdapter.AddEntry(tunnel.DNSURL); err != nil {
		m.removeTunnel(tunnel.DNSURL)
		m.proxyAdapter.RemoveRoute(tunnel.DNSURL)
		m.kubeAdapter.UnregisterServicePortForward(tunnel.Context, tunnel.Namespace, tunnel.Pod, tunnel.RemotePort)
		return fmt.Errorf("add hosts entry: %w", err)
	}

	return nil
}

func (m *DNSManager) UnregisterDNSTunnel(dnsURL string) error {
	if dnsURL == "" {
		return fmt.Errorf("DNS URL is required")
	}

	tunnel, found := m.removeTunnel(dnsURL)
	if !found {
		return fmt.Errorf("tunnel not found for DNS URL: %s", dnsURL)
	}

	if err := m.kubeAdapter.UnregisterServicePortForward(tunnel.Context, tunnel.Namespace, tunnel.Pod, tunnel.RemotePort); err != nil {
		if !strings.Contains(err.Error(), "port forward not found") {
			m.addTunnels([]DNSTunnel{tunnel})
			return fmt.Errorf("stop port forward: %w", err)
		}
	}

	m.proxyAdapter.RemoveRoute(dnsURL)

	if err := m.hostsFileAdapter.RemoveEntry(dnsURL); err != nil {
		m.addTunnels([]DNSTunnel{tunnel})
		m.proxyAdapter.AddRoute(tunnel.DNSURL, tunnel.LocalPort)
		return fmt.Errorf("remove hosts entry: %w", err)
	}

	return nil
}

func (m *DNSManager) Cleanup() error {
	m.kubeAdapter.StopAllPortForwards()

	if err := m.proxyAdapter.Stop(); err != nil {
		return fmt.Errorf("stop proxy server: %w", err)
	}

	if err := m.hostsFileAdapter.ClearAllEntries(); err != nil {
		return fmt.Errorf("clear hosts file entries: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.dnsTunnels = []DNSTunnel{}
	return nil
}

func (m *DNSManager) getUsedPorts() map[int32]bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	usedPorts := make(map[int32]bool, len(m.dnsTunnels))
	for _, t := range m.dnsTunnels {
		usedPorts[t.LocalPort] = true
	}
	return usedPorts
}

func (m *DNSManager) addTunnels(tunnels []DNSTunnel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dnsTunnels = append(m.dnsTunnels, tunnels...)
}

func (m *DNSManager) removeTunnel(dnsURL string) (DNSTunnel, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, t := range m.dnsTunnels {
		if t.DNSURL == dnsURL {
			m.dnsTunnels = append(m.dnsTunnels[:i], m.dnsTunnels[i+1:]...)
			return t, true
		}
	}
	return DNSTunnel{}, false
}

func convertToDNSTunnel(tunnel kube.ServiceTunnel) DNSTunnel {
	return DNSTunnel{
		Context:    tunnel.Context,
		Namespace:  tunnel.Namespace,
		DNSURL:     tunnel.DNSURL,
		Pod:        tunnel.Pod,
		LocalPort:  tunnel.LocalPort,
		RemotePort: tunnel.RemotePort,
	}
}
