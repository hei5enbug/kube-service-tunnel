package dns

import (
	"context"
	"errors"
	"fmt"

	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

// ErrUpdateCleanup marks an update that took effect although removing the old setting failed.
var ErrUpdateCleanup = errors.New("tunnel updated, but cleaning up the old setting failed")

type updatePlan struct {
	hostname           string
	localPort          int32
	requestedLocalPort int32
}

func (p updatePlan) hostChanged(old DNSTunnel) bool { return p.hostname != old.DNSURL }
func (p updatePlan) portMoved(old DNSTunnel) bool   { return p.localPort != old.LocalPort }

func (p updatePlan) unchanged(old DNSTunnel) bool {
	return !p.hostChanged(old) && !p.portMoved(old) && p.requestedLocalPort == old.RequestedLocalPort
}

// UpdateTunnel changes the hostname and local port of a live tunnel. An empty hostname restores the
// default service DNS name; a zero localPort selects an automatic port. The old setup keeps working
// until the new one is in place. When only cleaning up the old setting fails, the updated tunnel is
// returned together with an error wrapping ErrUpdateCleanup.
func (m *DNSManager) UpdateTunnel(ctx context.Context, dnsURL, hostname string, localPort int32) (DNSTunnel, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if m.closed {
		return DNSTunnel{}, errShuttingDown
	}

	old, found := m.findByDNS(dnsURL)
	if !found {
		return DNSTunnel{}, fmt.Errorf("tunnel not found for DNS URL: %s", dnsURL)
	}

	plan, err := m.planUpdate(old, hostname, localPort)
	if err != nil {
		return DNSTunnel{}, err
	}
	if plan.unchanged(old) {
		return old, nil
	}

	forwardKey := old.ForwardKey
	if plan.portMoved(old) {
		forwardKey, err = m.kubeAdapter.StartForward(ctx, old.Context, old.Namespace, old.Pod, old.RemotePort, plan.localPort)
		if err != nil {
			return DNSTunnel{}, fmt.Errorf("start port forward on 127.0.0.1:%d: %w", plan.localPort, err)
		}
	}

	if plan.hostChanged(old) {
		if err := m.hostsFileAdapter.AddEntry(plan.hostname); err != nil {
			addErr := fmt.Errorf("add hosts entry: %w", err)
			if plan.portMoved(old) {
				return DNSTunnel{}, errors.Join(addErr, m.stopForward(forwardKey))
			}
			return DNSTunnel{}, addErr
		}
	}

	return m.commitUpdate(old, plan, forwardKey)
}

func (m *DNSManager) planUpdate(old DNSTunnel, hostname string, localPort int32) (updatePlan, error) {
	plan := updatePlan{hostname: old.DNSURL, localPort: old.LocalPort}

	newHost, err := m.resolveHostname(old, hostname)
	if err != nil {
		return plan, err
	}
	plan.hostname = newHost

	if err := validateRequestedPort(localPort); err != nil {
		return plan, err
	}
	plan.requestedLocalPort = localPort
	plan.localPort, err = m.resolveLocalPort(old, localPort)
	return plan, err
}

func (m *DNSManager) resolveHostname(old DNSTunnel, hostname string) (string, error) {
	newHost := kube.BuildServiceDNS(old.Service, old.Namespace)
	if hostname != "" {
		normalized, err := NormalizeHostname(hostname)
		if err != nil {
			return "", err
		}
		newHost = normalized
	}
	if newHost == old.DNSURL {
		return newHost, nil
	}
	if owner, found := m.findByDNS(newHost); found {
		return "", fmt.Errorf("%s is already registered (context %s)", newHost, owner.Context)
	}
	return newHost, nil
}

func (m *DNSManager) resolveLocalPort(old DNSTunnel, requested int32) (int32, error) {
	switch {
	case requested == 0 && old.RequestedLocalPort == 0:
		return old.LocalPort, nil
	case requested == 0:
		return kube.NextFreeLocalPort(m.getUsedPorts())
	case requested == old.LocalPort:
		return old.LocalPort, nil
	}
	if err := kube.CheckLocalPort(requested, m.usedPortsExcept(old.ForwardKey)); err != nil {
		return 0, err
	}
	return requested, nil
}

// commitUpdate switches routing to the new setup, records it and releases the old setting. Failures while
// releasing are reported with ErrUpdateCleanup because the update itself already took effect.
func (m *DNSManager) commitUpdate(old DNSTunnel, plan updatePlan, forwardKey string) (DNSTunnel, error) {
	updated := old
	updated.DNSURL = plan.hostname
	updated.LocalPort = plan.localPort
	updated.RequestedLocalPort = plan.requestedLocalPort
	updated.ForwardKey = forwardKey

	m.proxyAdapter.AddRoute(plan.hostname, plan.localPort)
	var causes []error
	if plan.hostChanged(old) {
		m.proxyAdapter.RemoveRoute(old.DNSURL)
		if err := m.hostsFileAdapter.RemoveEntry(old.DNSURL); err != nil {
			causes = append(causes, fmt.Errorf("remove hosts entry %s: %w", old.DNSURL, err))
		}
	}
	m.replaceTunnel(old.ForwardKey, updated)

	if plan.portMoved(old) {
		if err := m.stopForward(old.ForwardKey); err != nil {
			causes = append(causes, fmt.Errorf("stop old port forward: %w", err))
		}
	}

	if len(causes) > 0 {
		return updated, errors.Join(append([]error{ErrUpdateCleanup}, causes...)...)
	}
	return updated, nil
}
