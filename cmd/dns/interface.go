package dns

import "github.com/byoungmin/kube-service-tunnel/internal/kube"

type DNSManagerInterface interface {
	GetAllDNSTunnels() []DNSTunnel
	RegisterAllByContext(contextName string, services []kube.Service) error
	RegisterDNSTunnel(contextName, serviceName, namespace string) error
	UnregisterDNSTunnel(dnsURL string) error
	Cleanup() error
}

var (
	_ DNSManagerInterface = (*DNSManager)(nil)
)
