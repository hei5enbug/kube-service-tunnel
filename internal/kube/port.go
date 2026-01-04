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

// NextFreeLocalPort returns the first automatically assignable local port that no tunnel uses.
func NextFreeLocalPort(usedPorts map[int32]bool) (int32, error) {
	return findAvailablePort(minLocalPort, usedPorts)
}

// CheckLocalPort reports why port cannot be used for a new forward on 127.0.0.1, or nil when it can.
func CheckLocalPort(port int32, usedPorts map[int32]bool) error {
	switch {
	case port < 1 || port > maxLocalPort:
		return fmt.Errorf("local port %d is out of range 1-65535", port)
	case usedPorts[port]:
		return fmt.Errorf("local port %d is used by another tunnel", port)
	case !isLocalPortFree(port):
		return fmt.Errorf("local port %d is already in use on 127.0.0.1", port)
	}
	return nil
}
