package dns

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"unicode/utf8"

	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

const (
	maxHostnameLength = 253
	maxLabelLength    = 63
	maxLocalPort      = 65535
)

// NormalizeHostname trims, lowercases and validates a custom hostname, stripping one trailing dot.
func NormalizeHostname(name string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(name))
	host = strings.TrimSuffix(host, ".")

	switch {
	case host == "":
		return "", errors.New("hostname must not be empty")
	case net.ParseIP(host) != nil:
		return "", errors.New("hostname must not be an IP address")
	case utf8.RuneCountInString(host) > maxHostnameLength:
		return "", fmt.Errorf("hostname must not be longer than %d characters", maxHostnameLength)
	}

	labels := strings.Split(host, ".")
	for _, label := range labels {
		if !validLabel(label) {
			return "", fmt.Errorf("hostname label %q is invalid: use letters, digits and hyphens", label)
		}
	}
	if isNumericLabel(labels[len(labels)-1]) {
		return "", errors.New("hostname must not end with a number, because browsers read it as an IP address")
	}
	return host, nil
}

// isNumericLabel reports a last label that a browser URL parser treats as part of an IPv4 address, such as
// "1" in "127.1" or the hexadecimal "0x1f".
func isNumericLabel(label string) bool {
	digits := label
	isHex := strings.HasPrefix(label, "0x")
	if isHex {
		digits = label[len("0x"):]
	}
	for _, r := range digits {
		if r >= '0' && r <= '9' || isHex && r >= 'a' && r <= 'f' {
			continue
		}
		return false
	}
	return isHex || digits != ""
}

func validLabel(label string) bool {
	if label == "" || len(label) > maxLabelLength {
		return false
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, r := range label {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// validateRequestedPort accepts 0 (automatic) or a port other than the proxy's.
func validateRequestedPort(port int32) error {
	switch {
	case port == 0:
		return nil
	case port < 1 || port > maxLocalPort:
		return fmt.Errorf("local port %d is out of range 1-65535", port)
	case port == proxyPort:
		return fmt.Errorf("local port %d is used by the proxy", port)
	}
	return nil
}

// normalizeTarget validates the custom hostname and local port of a target and returns it with the
// hostname normalized.
func normalizeTarget(target kube.ServiceRef) (kube.ServiceRef, error) {
	if target.Hostname != "" {
		host, err := NormalizeHostname(target.Hostname)
		if err != nil {
			return target, err
		}
		target.Hostname = host
	}
	if err := validateRequestedPort(target.LocalPort); err != nil {
		return target, err
	}
	return target, nil
}
