package dns

import "sync"

var (
	dnsManagerInstance *dnsManager
	dnsManagerOnce     sync.Once
)

func NewDNSManagerInstance() DNSManagerInterface {
	dnsManagerOnce.Do(initDNSManager)
	return dnsManagerInstance
}

func initDNSManager() {
	hostsFileManager := newHostsFileManager()
	dnsManagerInstance = newDNSManager(hostsFileManager)
}
