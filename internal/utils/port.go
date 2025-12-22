package utils

import (
	"fmt"
	"net"
)

func FindAvailablePort(startPort int32, usedPorts map[int32]bool) (int32, error) {
	if startPort < 40000 {
		startPort = 40000
	}

	for port := startPort; port < 65535; port++ {
		if usedPorts[port] {
			continue
		}

		addr, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			continue
		}
		addr.Close()
		return port, nil
	}

	return 0, fmt.Errorf("no available port found starting from %d", startPort)
}

