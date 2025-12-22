package utils

import (
	"fmt"
	"strings"
)

func GenerateServiceDNS(serviceName, namespace string) string {
	return fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, namespace)
}

func ExtractHostsDomain(serviceURL string) string {
	if strings.Contains(serviceURL, ":") {
		parts := strings.Split(serviceURL, ":")
		return parts[0]
	}
	return serviceURL
}

func IsHTTPPort(port int32) bool {
	httpPorts := []int32{80, 8080, 3000, 8000, 9000}
	for _, p := range httpPorts {
		if port == p {
			return true
		}
	}
	return false
}

