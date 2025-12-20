package dns

import "fmt"

type DNSManagerInterface interface {
	AddService(namespace, serviceName, ip string) error
	RemoveService(namespace, serviceName string) error
	WriteServices(entries map[string]string) error
	Cleanup() error
}

type dnsManager struct {
	hostsFileManager *hostsFileManager
}

func newDNSManager(hostsFileManager *hostsFileManager) *dnsManager {
	return &dnsManager{
		hostsFileManager: hostsFileManager,
	}
}

func (d *dnsManager) AddService(namespace, serviceName, ip string) error {
	dnsName := d.buildServiceDNSName(namespace, serviceName)
	entries := map[string]string{
		dnsName: ip,
	}
	return d.hostsFileManager.WriteEntries(entries)
}

func (d *dnsManager) RemoveService(namespace, serviceName string) error {
	return d.Cleanup()
}

func (d *dnsManager) WriteServices(entries map[string]string) error {
	return d.hostsFileManager.WriteEntries(entries)
}

func (d *dnsManager) Cleanup() error {
	return d.hostsFileManager.WriteEntries(nil)
}

func (d *dnsManager) buildServiceDNSName(namespace, serviceName string) string {
	return fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, namespace)
}

