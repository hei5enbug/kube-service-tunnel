package kube

import (
	"fmt"
	"net"
)

const (
	minLocalPort = 40000
	maxLocalPort = 65535
)

func findAvailablePort(startPort int32, usedPorts map[int32]bool) (int32, error) {
	if startPort < minLocalPort {
		startPort = minLocalPort
	}

	for port := startPort; port <= maxLocalPort; port++ {
		if usedPorts[port] || !isLocalPortFree(port) {
			continue
		}
		return port, nil
	}

	return 0, fmt.Errorf("no available port found starting from %d", startPort)
}

func isLocalPortFree(port int32) bool {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}
