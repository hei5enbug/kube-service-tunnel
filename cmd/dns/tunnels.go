package dns

import (
	"strings"

	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

func (m *DNSManager) getUsedPorts() map[int32]bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	usedPorts := make(map[int32]bool, len(m.dnsTunnels))
	for _, t := range m.dnsTunnels {
		usedPorts[t.LocalPort] = true
	}
	return usedPorts
}

func (m *DNSManager) registeredOwners() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	owners := make(map[string]string, len(m.dnsTunnels))
	for _, t := range m.dnsTunnels {
		owners[t.DNSURL] = t.Context
	}
	return owners
}

func (m *DNSManager) addTunnel(tunnel DNSTunnel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dnsTunnels = append(m.dnsTunnels, tunnel)
}

func (m *DNSManager) removeTunnel(dnsURL string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	remaining := make([]DNSTunnel, 0, len(m.dnsTunnels))
	for _, t := range m.dnsTunnels {
		if t.DNSURL != dnsURL {
			remaining = append(remaining, t)
		}
	}
	m.dnsTunnels = remaining
}

func (m *DNSManager) findByDNS(dnsURL string) (DNSTunnel, bool) {
	return m.find(func(t DNSTunnel) bool { return t.DNSURL == dnsURL })
}

func (m *DNSManager) findByForwardKey(forwardKey string) (DNSTunnel, bool) {
	return m.find(func(t DNSTunnel) bool { return t.ForwardKey == forwardKey })
}

func (m *DNSManager) find(match func(DNSTunnel) bool) (DNSTunnel, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, t := range m.dnsTunnels {
		if match(t) {
			return t, true
		}
	}
	return DNSTunnel{}, false
}

func serviceName(tunnel kube.ServiceTunnel) string {
	return strings.TrimSuffix(tunnel.DNSURL, "."+strings.ToLower(tunnel.Namespace))
}
