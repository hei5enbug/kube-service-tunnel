package dns

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

type hostsFileManager struct {
	startMarker string
	endMarker   string
}

func newHostsFileManager() *hostsFileManager {
	return &hostsFileManager{
		startMarker: "# kube-service-tunnel entries",
		endMarker:   "# end kube-service-tunnel entries",
	}
}

func (h *hostsFileManager) WriteEntries(entries map[string]string) error {
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

	tmpPath := hostsPath + ".tmp"
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
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(tmpPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write temp hosts file: %w", err)
	}
	return nil
}

func (h *hostsFileManager) copyToHostsFile(tmpPath, hostsPath string) error {
	cmd := exec.Command("sudo", "cp", tmpPath, hostsPath)
	if err := cmd.Run(); err != nil {
		if removeErr := os.Remove(tmpPath); removeErr != nil {
			log.Printf("failed to remove temp file %s: %v", tmpPath, removeErr)
		}
		return fmt.Errorf("update hosts file (sudo required): %w", err)
	}

	if err := os.Remove(tmpPath); err != nil {
		log.Printf("failed to remove temp file %s: %v", tmpPath, err)
	}
	return nil
}
