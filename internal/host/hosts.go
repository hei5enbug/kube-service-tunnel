package host

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type HostsFileManagerInterface interface {
	WriteEntries(entries map[string]string) error
	ReadExistingEntries() (map[string]string, error)
	ClearAllEntries() error
}

type hostsFileManager struct {
	startMarker string
	endMarker   string
	mu          sync.Mutex
}

func newHostsFileManager() *hostsFileManager {
	return &hostsFileManager{
		startMarker: "# Added by kube-service-tunnel",
		endMarker:   "# End of section",
	}
}

func (h *hostsFileManager) WriteEntries(entries map[string]string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	const hostsPath = "/etc/hosts"

	lines, err := h.readHostsFile(hostsPath)
	if err != nil {
		return err
	}

	var newLines []string
	inTunnelSection := false
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == h.startMarker {
			inTunnelSection = true
			continue
		}
		if trimmedLine == h.endMarker {
			inTunnelSection = false
			continue
		}
		if inTunnelSection {
			continue
		}
		newLines = append(newLines, line)
	}

	if len(entries) > 0 {
		newLines = append(newLines, "")
		newLines = append(newLines, h.startMarker)
		newLines = append(newLines, "# This section is automatically managed by kube-service-tunnel")
		for dnsName, ip := range entries {
			newLines = append(newLines, fmt.Sprintf("%s\t%s", ip, dnsName))
		}
		newLines = append(newLines, h.endMarker)
	}

		tmpPath := filepath.Join(os.TempDir(), "kube-service-tunnel-hosts.tmp")
		if err := h.writeTempFile(tmpPath, newLines); err != nil {
			return err
		}

	return h.copyToHostsFile(tmpPath, hostsPath)
}

func (h *hostsFileManager) readHostsFile(hostsPath string) ([]string, error) {
	content, err := os.ReadFile(hostsPath)
	if err != nil {
		return nil, fmt.Errorf("read hosts file: %w", err)
	}
	return strings.Split(string(content), "\n"), nil
}

func (h *hostsFileManager) writeTempFile(tmpPath string, lines []string) error {
	content := strings.Join(lines, "\n")
	content = strings.TrimRight(content, "\n") + "\n"
	if err := os.WriteFile(tmpPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write temp hosts file: %w", err)
	}
	return nil
}

func (h *hostsFileManager) copyToHostsFile(tmpPath, hostsPath string) error {
	content, err := os.ReadFile(tmpPath)
	if err != nil {
		return fmt.Errorf("read temp file: %w", err)
	}
	if err := os.WriteFile(hostsPath, content, 0644); err != nil {
		if removeErr := os.Remove(tmpPath); removeErr != nil {
			log.Printf("failed to remove temp file %s: %v", tmpPath, removeErr)
		}
		return fmt.Errorf("failed to write hosts file: %w", err)
	}
	
	if err := os.Remove(tmpPath); err != nil {
		log.Printf("failed to remove temp file %s: %v", tmpPath, err)
	}
	return nil
}

func (h *hostsFileManager) ReadExistingEntries() (map[string]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	const hostsPath = "/etc/hosts"
	
	lines, err := h.readHostsFile(hostsPath)
	if err != nil {
		return nil, err
	}
	
	entries := make(map[string]string)
	inTunnelSection := false
	
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == h.startMarker {
			inTunnelSection = true
			continue
		}
		if trimmedLine == h.endMarker {
			inTunnelSection = false
			continue
		}
		if inTunnelSection && !strings.HasPrefix(trimmedLine, "#") && trimmedLine != "" {
			parts := strings.Fields(trimmedLine)
			if len(parts) >= 2 {
				ip := parts[0]
				dnsName := parts[1]
				entries[dnsName] = ip
			}
		}
	}
	
	return entries, nil
}

func (h *hostsFileManager) ClearAllEntries() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	const hostsPath = "/etc/hosts"

	lines, err := h.readHostsFile(hostsPath)
	if err != nil {
		return err
	}

	var newLines []string
	inTunnelSection := false
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == h.startMarker {
			inTunnelSection = true
			continue
		}
		if trimmedLine == h.endMarker {
			inTunnelSection = false
			continue
		}
		if inTunnelSection {
			continue
		}
		newLines = append(newLines, line)
	}

	tmpPath := filepath.Join(os.TempDir(), "kube-service-tunnel-hosts.tmp")
	if err := h.writeTempFile(tmpPath, newLines); err != nil {
		return err
	}

	return h.copyToHostsFile(tmpPath, hostsPath)
}

