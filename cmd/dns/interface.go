package dns

import (
	"context"

	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

type DNSTunnel struct {
	Context            string
	Namespace          string
	Service            string
	DNSURL             string
	Pod                string
	LocalPort          int32
	RequestedLocalPort int32
	RemotePort         int32
	ForwardKey         string
}

type BulkRegistrationResult struct {
	Registered []DNSTunnel
	Failures   []kube.ServiceRegistrationFailure
	Skipped    []kube.ServiceRegistrationFailure
	Ineligible []kube.ServiceRegistrationFailure
}

type DNSManagerInterface interface {
	GetAllDNSTunnels() []DNSTunnel
	RegisterServices(ctx context.Context, contextName string, targets []kube.ServiceRef, progress func(kube.RegistrationProgress)) (BulkRegistrationResult, error)
	RegisterDNSTunnel(ctx context.Context, contextName string, target kube.ServiceRef) (DNSTunnel, error)
	UpdateTunnel(ctx context.Context, dnsURL, hostname string, localPort int32) (DNSTunnel, error)
	UnregisterDNSTunnel(dnsURL string) error
	SetTunnelLostHandler(handler func(tunnel DNSTunnel, err error))
	Cleanup() error
}

var (
	_ DNSManagerInterface = (*DNSManager)(nil)
)
