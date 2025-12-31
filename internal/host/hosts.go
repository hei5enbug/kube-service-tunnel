package host

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

const (
	defaultHostsPath     = "/etc/hosts"
	loopbackIP           = "127.0.0.1"
	startMarker          = "# Added by kube-service-tunnel"
	endMarker            = "# End of kube-service-tunnel section"
	legacyEndMarker      = "# End of section"
	foreignSectionPrefix = "# Added by "
	sectionNotice        = "# This section is automatically managed by kube-service-tunnel"
	tempFilePattern      = ".kube-service-tunnel-hosts-*"
	hostsFallbackFormat  = "rename hosts file: %v; in-place write: %w"
)

var errMalformedSection = errors.New("hosts file has a malformed kube-service-tunnel section (missing end marker); fix the file manually")

type HostsFileAdapterInterface interface {
	AddEntry(dnsURL string) error
	RemoveEntry(dnsURL string) error
	ClearAllEntries() error
}

type hostsFileAdapter struct {
	hostsPath string
	mu        sync.Mutex
}

func NewHostsFileAdapter() *hostsFileAdapter {
	return newHostsFileAdapterWithPath(defaultHostsPath)
}

func newHostsFileAdapterWithPath(path string) *hostsFileAdapter {
	return &hostsFileAdapter{hostsPath: path}
}

// section holds the line indexes of our start and end markers. start is -1 when absent.
type section struct {
	start int
	end   int
}

func (s section) exists() bool {
	return s.start >= 0
}

func (s section) contains(i int) bool {
	return s.exists() && i >= s.start && i <= s.end
}

func (h *hostsFileAdapter) AddEntry(dnsURL string) error {
	host, err := normalizeHostname(dnsURL)
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	return h.update(func(lines []string, sec section) ([]string, bool, error) {
		if findConflict(lines, sec, host) {
			return nil, false, fmt.Errorf("hostname %q is already mapped outside the kube-service-tunnel section", host)
		}
		if sectionHasHost(lines, sec, host) {
			return nil, false, nil
		}
		return insertEntry(lines, sec, host), true, nil
	})
}

func (h *hostsFileAdapter) RemoveEntry(dnsURL string) error {
	host, err := normalizeHostname(dnsURL)
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	return h.update(func(lines []string, sec section) ([]string, bool, error) {
		if !sec.exists() || !sectionHasHost(lines, sec, host) {
			return nil, false, nil
		}
		kept := make([]string, 0, len(lines))
		for i, line := range lines {
			if isInsideBody(sec, i) && lineHasHost(line, host) {
				continue
			}
			kept = append(kept, line)
		}
		return kept, true, nil
	})
}

func (h *hostsFileAdapter) ClearAllEntries() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.update(func(lines []string, sec section) ([]string, bool, error) {
		if !sec.exists() {
			return nil, false, nil
		}
		kept := make([]string, 0, len(lines))
		kept = append(kept, lines[:sec.start]...)
		kept = append(kept, lines[sec.end+1:]...)
		return kept, true, nil
	})
}

// update reads the hosts file, locates our section, applies mutate and writes the result when changed.
func (h *hostsFileAdapter) update(mutate func(lines []string, sec section) ([]string, bool, error)) error {
	content, err := os.ReadFile(h.hostsPath)
	if err != nil {
		return fmt.Errorf("read hosts file: %w", err)
	}
	lines := strings.Split(string(content), "\n")

	sec, err := findSection(lines)
	if err != nil {
		return err
	}

	newLines, changed, err := mutate(lines, sec)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return writeHostsFile(h.hostsPath, renderLines(newLines))
}

func normalizeHostname(name string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(name))
	if host == "" {
		return "", errors.New("hostname must not be empty")
	}
	if strings.ContainsAny(host, " \t\r\n#") {
		return "", fmt.Errorf("invalid hostname %q", name)
	}
	return host, nil
}

func isEndMarker(trimmed string) bool {
	return trimmed == endMarker || trimmed == legacyEndMarker
}

// findSection locates our section. An end marker only counts while inside our section.
func findSection(lines []string) (section, error) {
	sec := section{start: -1, end: -1}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !sec.exists() {
			if trimmed == startMarker {
				sec.start = i
			}
			continue
		}
		if isEndMarker(trimmed) {
			sec.end = i
			return sec, nil
		}
		if strings.HasPrefix(trimmed, foreignSectionPrefix) {
			return section{}, errMalformedSection
		}
	}
	if sec.exists() {
		return section{}, errMalformedSection
	}
	return sec, nil
}

func isInsideBody(sec section, i int) bool {
	return sec.exists() && i > sec.start && i < sec.end
}

// lineAliases returns the lowercase hostnames mapped by a hosts line, ignoring comments.
func lineAliases(line string) []string {
	if idx := strings.Index(line, "#"); idx >= 0 {
		line = line[:idx]
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return nil
	}
	aliases := make([]string, 0, len(fields)-1)
	for _, f := range fields[1:] {
		aliases = append(aliases, strings.ToLower(f))
	}
	return aliases
}

func lineHasHost(line, host string) bool {
	for _, alias := range lineAliases(line) {
		if alias == host {
			return true
		}
	}
	return false
}

func findConflict(lines []string, sec section, host string) bool {
	for i, line := range lines {
		if sec.contains(i) {
			continue
		}
		if lineHasHost(line, host) {
			return true
		}
	}
	return false
}

func sectionHasHost(lines []string, sec section, host string) bool {
	for i, line := range lines {
		if isInsideBody(sec, i) && lineHasHost(line, host) {
			return true
		}
	}
	return false
}

func insertEntry(lines []string, sec section, host string) []string {
	entry := fmt.Sprintf("%s\t%s", loopbackIP, host)
	if sec.exists() {
		result := make([]string, 0, len(lines)+1)
		result = append(result, lines[:sec.end]...)
		result = append(result, entry)
		result = append(result, lines[sec.end:]...)
		return result
	}
	result := make([]string, 0, len(lines)+5)
	result = append(result, trimTrailingBlank(lines)...)
	return append(result, "", startMarker, sectionNotice, entry, endMarker)
}

func trimTrailingBlank(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[:end]
}

func renderLines(lines []string) string {
	trimmed := trimTrailingBlank(lines)
	if len(trimmed) == 0 {
		return ""
	}
	return strings.Join(trimmed, "\n") + "\n"
}

// writeHostsFile replaces the file atomically, falling back to an in-place write when rename is not possible.
func writeHostsFile(path, content string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve hosts file path: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("stat hosts file: %w", err)
	}

	tmpPath, err := writeTempBeside(resolved, info, content)
	if err != nil {
		return err
	}
	renameErr := os.Rename(tmpPath, resolved)
	if renameErr == nil {
		return nil
	}
	_ = os.Remove(tmpPath)

	if err := os.WriteFile(resolved, []byte(content), info.Mode().Perm()); err != nil {
		return fmt.Errorf(hostsFallbackFormat, renameErr, err)
	}
	return nil
}

// writeTempBeside creates a synced temp file next to target carrying target's mode and owner.
func writeTempBeside(target string, info os.FileInfo, content string) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(target), tempFilePattern)
	if err != nil {
		return "", fmt.Errorf("create temp hosts file: %w", err)
	}
	tmpPath := tmp.Name()

	if err := fillTemp(tmp, info, content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close temp hosts file: %w", err)
	}
	return tmpPath, nil
}

func fillTemp(tmp *os.File, info os.FileInfo, content string) error {
	if _, err := tmp.WriteString(content); err != nil {
		return fmt.Errorf("write temp hosts file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temp hosts file: %w", err)
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		return fmt.Errorf("chmod temp hosts file: %w", err)
	}
	if os.Geteuid() != 0 {
		return nil
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		if err := tmp.Chown(int(stat.Uid), int(stat.Gid)); err != nil {
			return fmt.Errorf("chown temp hosts file: %w", err)
		}
	}
	return nil
}
