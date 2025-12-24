package dns

import (
	"fmt"

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
		dnsTunnels:       []DNSTunnel{},
	}

	return dnsManager, nil
}

func (m *DNSManager) GetAllDNSTunnels() []DNSTunnel {
	return m.dnsTunnels
}

func (m *DNSManager) RegisterAllByContext(contextName string) error {
	if contextName == "" {
		return fmt.Errorf("context name is required")
	}

	usedPorts := extractUsedPorts(m.dnsTunnels)

	tunnels, err := m.kubeAdapter.RegisterAllServicesForContext(contextName, usedPorts)
	if err != nil {
		return err
	}

	if err := m.proxyAdapter.StartIfNotRunning(80); err != nil {
		return fmt.Errorf("start proxy server: %w", err)
	}

	routes := make(map[string]int32)
	for _, tunnel := range tunnels {
		m.dnsTunnels = append(m.dnsTunnels, convertToDNSTunnel(tunnel))
		routes[tunnel.DNSURL] = tunnel.LocalPort
	}

	m.proxyAdapter.AddRoutes(routes)

	for _, tunnel := range tunnels {
		if err := m.hostsFileAdapter.AddEntry(tunnel.DNSURL); err != nil {
			return fmt.Errorf("add hosts entry: %w", err)
		}
	}

	return nil
}

func (m *DNSManager) RegisterDNSTunnel(contextName, serviceName, namespace string) error {
	if contextName == "" || serviceName == "" || namespace == "" {
		return fmt.Errorf("context name, service name and namespace are required")
	}

	usedPorts := extractUsedPorts(m.dnsTunnels)

	tunnel, err := m.kubeAdapter.RegisterServicePortForward(contextName, serviceName, namespace, usedPorts)
	if err != nil {
		return err
	}

	if err := m.proxyAdapter.StartIfNotRunning(80); err != nil {
		return fmt.Errorf("start proxy server: %w", err)
	}

	m.proxyAdapter.AddRoute(tunnel.DNSURL, tunnel.LocalPort)

	m.dnsTunnels = append(m.dnsTunnels, convertToDNSTunnel(tunnel))

	if err := m.hostsFileAdapter.AddEntry(tunnel.DNSURL); err != nil {
		return fmt.Errorf("add hosts entry: %w", err)
	}

	return nil
}

func (m *DNSManager) UnregisterDNSTunnel(dnsURL string) error {
	if dnsURL == "" {
		return fmt.Errorf("DNS URL is required")
	}

	index := -1
	var tunnel DNSTunnel
	for i, t := range m.dnsTunnels {
		if t.DNSURL == dnsURL {
			index = i
			tunnel = t
			break
		}
	}
	if index == -1 {
		return fmt.Errorf("tunnel not found for DNS URL: %s", dnsURL)
	}

	if err := m.kubeAdapter.UnregisterServicePortForward(tunnel.Context, tunnel.Namespace, tunnel.Pod, tunnel.RemotePort); err != nil {
		return fmt.Errorf("stop port forward: %w", err)
	}

	m.proxyAdapter.RemoveRoute(dnsURL)

	m.dnsTunnels = append(m.dnsTunnels[:index], m.dnsTunnels[index+1:]...)

	if err := m.hostsFileAdapter.RemoveEntry(dnsURL); err != nil {
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

	m.dnsTunnels = []DNSTunnel{}
	return nil
}

func extractUsedPorts(tunnels []DNSTunnel) map[int32]bool {
	usedPorts := make(map[int32]bool)
	for _, t := range tunnels {
		usedPorts[t.LocalPort] = true
	}
	return usedPorts
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
