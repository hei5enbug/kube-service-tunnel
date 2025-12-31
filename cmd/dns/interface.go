package dns

import (
	"context"

	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

type DNSTunnel struct {
	Context    string
	Namespace  string
	DNSURL     string
	Pod        string
	LocalPort  int32
	RemotePort int32
	ForwardKey string
}

type BulkRegistrationResult struct {
	Registered []DNSTunnel
	Failures   []kube.ServiceRegistrationFailure
}

type DNSManagerInterface interface {
	GetAllDNSTunnels() []DNSTunnel
	RegisterAllByContext(ctx context.Context, contextName string, namespaces []string) (BulkRegistrationResult, error)
	RegisterDNSTunnel(ctx context.Context, contextName, serviceName, namespace string) error
	UnregisterDNSTunnel(dnsURL string) error
	SetTunnelLostHandler(handler func(tunnel DNSTunnel, err error))
	Cleanup() error
}

var (
	_ DNSManagerInterface = (*DNSManager)(nil)
)
