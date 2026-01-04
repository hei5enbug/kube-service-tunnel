package tui

import (
	"fmt"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

// tunnelIndex finds the tunnel of a service and the tunnel that uses a hostname.
type tunnelIndex struct {
	byService map[string]dns.DNSTunnel
	byHost    map[string]dns.DNSTunnel
}

func indexTunnels(tunnels []dns.DNSTunnel) tunnelIndex {
	index := tunnelIndex{
		byService: make(map[string]dns.DNSTunnel, len(tunnels)),
		byHost:    make(map[string]dns.DNSTunnel, len(tunnels)),
	}
	for _, tunnel := range tunnels {
		index.byService[tunnelSettingKey(tunnel)] = tunnel
		index.byHost[tunnel.DNSURL] = tunnel
	}
	return index
}

func tunnelSettingKey(tunnel dns.DNSTunnel) string {
	return store.SettingKey(tunnel.Context, tunnel.Namespace, tunnel.Service)
}

// serviceLabel names a service the way the status line and dialogs show it, such as "dev api/web".
func serviceLabel(contextName, namespace, name string) string {
	return fmt.Sprintf("%s %s/%s", contextName, namespace, name)
}

func tunnelLabel(tunnel dns.DNSTunnel) string {
	return serviceLabel(tunnel.Context, tunnel.Namespace, tunnel.Service)
}

func defaultTunnelHostname(tunnel dns.DNSTunnel) string {
	return kube.BuildServiceDNS(tunnel.Service, tunnel.Namespace)
}

func localAddress(port int32) string {
	return fmt.Sprintf("127.0.0.1:%d", port)
}

// serviceTarget builds the registration target of a service from its remembered setting.
func serviceTarget(svc kube.Service, setting store.TunnelSetting) kube.ServiceRef {
	return kube.ServiceRef{Namespace: svc.Namespace, Name: svc.Name, Hostname: setting.Hostname, LocalPort: setting.LocalPort}
}

// portFallback describes a tunnel whose requested local port was busy, so the backend chose another one.
func portFallback(tunnel dns.DNSTunnel) (string, bool) {
	if tunnel.RequestedLocalPort == 0 || tunnel.LocalPort == tunnel.RequestedLocalPort {
		return "", false
	}
	return fmt.Sprintf("local port %d was busy, using %d", tunnel.RequestedLocalPort, tunnel.LocalPort), true
}
