package host

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type HostsFileAdapterInterface interface {
	AddEntry(dnsURL string) error
	RemoveEntry(dnsURL string) error
	ClearAllEntries() error
}

type hostsFileAdapter struct {
	startMarker string
	endMarker   string
	mu          sync.Mutex
}

func NewHostsFileAdapter() *hostsFileAdapter {
	return &hostsFileAdapter{
		startMarker: "# Added by kube-service-tunnel",
		endMarker:   "# End of section",
	}
}

func (h *hostsFileAdapter) AddEntry(dnsURL string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	const hostsPath = "/etc/hosts"
	const ip = "127.0.0.1"

	lines, err := h.readHostsFile(hostsPath)
	if err != nil {
		return err
	}

	var newLines []string
	inTunnelSection := false
	entryExists := false
	sectionStartIndex := -1
	sectionEndIndex := -1

	for i, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == h.startMarker {
			inTunnelSection = true
			sectionStartIndex = i
			newLines = append(newLines, line)
			continue
		}
		if trimmedLine == h.endMarker {
			sectionEndIndex = i
			inTunnelSection = false
			newLines = append(newLines, line)
			continue
		}
		if inTunnelSection {
			parts := strings.Fields(trimmedLine)
			if len(parts) >= 2 && parts[1] == dnsURL {
				entryExists = true
			}
			newLines = append(newLines, line)
			continue
		}
		newLines = append(newLines, line)
	}

	if entryExists {
		return nil
	}

	if sectionStartIndex == -1 {
		newLines = append(newLines, "")
		newLines = append(newLines, h.startMarker)
		newLines = append(newLines, "# This section is automatically managed by kube-service-tunnel")
		newLines = append(newLines, fmt.Sprintf("%s\t%s", ip, dnsURL))
		newLines = append(newLines, h.endMarker)
	} else {
		insertIndex := sectionEndIndex
		if insertIndex == -1 {
			insertIndex = len(newLines)
		}
		newLines = append(newLines[:insertIndex], append([]string{fmt.Sprintf("%s\t%s", ip, dnsURL)}, newLines[insertIndex:]...)...)
	}

	tmpPath := filepath.Join(os.TempDir(), "kube-service-tunnel-hosts.tmp")
	if err := h.writeTempFile(tmpPath, newLines); err != nil {
		return err
	}

	return h.copyToHostsFile(tmpPath, hostsPath)
}

func (h *hostsFileAdapter) RemoveEntry(dnsURL string) error {
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
			newLines = append(newLines, line)
			continue
		}
		if trimmedLine == h.endMarker {
			inTunnelSection = false
			newLines = append(newLines, line)
			continue
		}
		if inTunnelSection {
			parts := strings.Fields(trimmedLine)
			if len(parts) >= 2 && parts[1] == dnsURL {
				continue
			}
			newLines = append(newLines, line)
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

func (h *hostsFileAdapter) ClearAllEntries() error {
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

func (h *hostsFileAdapter) readHostsFile(hostsPath string) ([]string, error) {
	content, err := os.ReadFile(hostsPath)
	if err != nil {
		return nil, fmt.Errorf("read hosts file: %w", err)
	}
	return strings.Split(string(content), "\n"), nil
}

func (h *hostsFileAdapter) writeTempFile(tmpPath string, lines []string) error {
	content := strings.Join(lines, "\n")
	content = strings.TrimRight(content, "\n") + "\n"
	if err := os.WriteFile(tmpPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write temp hosts file: %w", err)
	}
	return nil
}

func (h *hostsFileAdapter) copyToHostsFile(tmpPath, hostsPath string) error {
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
