package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
)

const (
	maxLocalPort  = 65535
	proxyPort     = 80
	maxPortDigits = 5
)

type editField int

const (
	editHostnameField editField = iota
	editPortField
)

// editRequest is a validated edit. An empty hostname restores the default and a zero port selects an automatic
// one.
type editRequest struct {
	tunnel   dns.DNSTunnel
	hostname string
	port     int32
}

func (r editRequest) setting() store.TunnelSetting {
	return store.TunnelSetting{Hostname: r.hostname, LocalPort: r.port}
}

// editPrefill returns the field texts for a tunnel: the hostname only when it is custom, and the local port only
// when one was requested.
func editPrefill(tunnel dns.DNSTunnel) (string, string) {
	hostname, port := "", ""
	if tunnel.DNSURL != defaultTunnelHostname(tunnel) {
		hostname = tunnel.DNSURL
	}
	if tunnel.RequestedLocalPort != 0 {
		port = strconv.Itoa(int(tunnel.RequestedLocalPort))
	}
	return hostname, port
}

func acceptPortText(text string, _ rune) bool {
	return len(text) <= maxPortDigits && !strings.ContainsFunc(text, func(r rune) bool { return r < '0' || r > '9' })
}

// parseEdit validates the field texts and names the field to fix when one is invalid.
func parseEdit(tunnel dns.DNSTunnel, hostnameText, portText string) (editRequest, editField, error) {
	hostname, err := parseHostname(hostnameText, defaultTunnelHostname(tunnel))
	if err != nil {
		return editRequest{}, editHostnameField, err
	}
	port, err := parseLocalPort(portText)
	if err != nil {
		return editRequest{}, editPortField, err
	}
	return editRequest{tunnel: tunnel, hostname: hostname, port: port}, editHostnameField, nil
}

// parseHostname returns "" for an empty field or the default hostname, and the normalized hostname otherwise.
func parseHostname(text, defaultHostname string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", nil
	}
	hostname, err := dns.NormalizeHostname(text)
	if err != nil {
		return "", err
	}
	if hostname == defaultHostname {
		return "", nil
	}
	return hostname, nil
}

// parseLocalPort returns 0 for an empty field. Port 80 belongs to the proxy that serves every hostname.
func parseLocalPort(text string) (int32, error) {
	if text == "" {
		return 0, nil
	}
	port, err := strconv.ParseInt(text, 10, 32)
	switch {
	case err != nil:
		return 0, fmt.Errorf("local port %q is not a number", text)
	case port < 1 || port > maxLocalPort:
		return 0, fmt.Errorf("local port %d is out of range 1-65535", port)
	case port == proxyPort:
		return 0, fmt.Errorf("local port %d is used by the proxy", port)
	}
	return int32(port), nil
}

func (a *App) startUpdate(request editRequest) {
	if !a.store.TryBeginLoading("Updating " + tunnelURL(request.tunnel) + "…") {
		return
	}
	go a.runUpdate(request)
}

// runUpdate remembers the setting only when the update took effect, including when just the cleanup failed.
func (a *App) runUpdate(request editRequest) {
	defer a.store.SetLoading(false)
	updated, err := a.manager.UpdateTunnel(a.ctx, request.tunnel.DNSURL, request.hostname, request.port)
	if err == nil || errors.Is(err, dns.ErrUpdateCleanup) {
		a.store.SetTunnelSetting(tunnelSettingKey(request.tunnel), request.setting())
	}
	a.store.TunnelsChanged()
	a.store.Notify(updateReport(request.tunnel, updated, err))
}

func updateReport(old, updated dns.DNSTunnel, err error) (store.Level, string) {
	switch {
	case errors.Is(err, dns.ErrUpdateCleanup):
		return store.LevelWarning, updatedText(old, updated) + " · " + err.Error()
	case err != nil:
		return store.LevelError, fmt.Sprintf("Updating %s failed: %v", tunnelURL(old), err)
	case updated == old:
		return store.LevelInfo, "Nothing changed for " + tunnelURL(old)
	default:
		return store.LevelSuccess, updatedText(old, updated)
	}
}

// updatedText reads like "Updated http://web.api → http://api.local · 127.0.0.1:8080".
func updatedText(old, updated dns.DNSTunnel) string {
	text := "Updated " + tunnelURL(old)
	if updated.DNSURL != old.DNSURL {
		text += " → " + tunnelURL(updated)
	}
	text += " · " + localAddress(updated.LocalPort)
	if updated.RequestedLocalPort == 0 {
		text += " (auto)"
	}
	return text
}
