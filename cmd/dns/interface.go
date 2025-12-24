package dns

type DNSManagerInterface interface {
	GetAllDNSTunnels() []DNSTunnel

	RegisterAllByContext(contextName string) error
	RegisterDNSTunnel(contextName, serviceName, namespace string) error
	UnregisterDNSTunnel(dnsURL string) error

	Cleanup() error
}

var (
	_ DNSManagerInterface = (*DNSManager)(nil)
)
