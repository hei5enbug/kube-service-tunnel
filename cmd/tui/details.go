package tui

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
	"github.com/rivo/tview"
)

const detailsPage = "details"

type detail struct {
	label string
	value string
}

// detailText aligns labels and escapes values; continuation lines of a value are indented under it.
func detailText(details []detail) string {
	labelWidth := 0
	for _, d := range details {
		labelWidth = max(labelWidth, displayWidth(d.label))
	}
	var lines []string
	for _, d := range details {
		valueLines := strings.Split(tview.Escape(d.value), "\n")
		lines = append(lines, fmt.Sprintf("%-*s  %s", labelWidth, d.label, valueLines[0]))
		for _, line := range valueLines[1:] {
			lines = append(lines, strings.Repeat(" ", labelWidth+2)+line)
		}
	}
	return strings.Join(lines, "\n")
}

func (a *App) showDetails(p *pane) {
	details, ok := a.detailsFor(p.area)
	if !ok {
		a.store.Notify(store.LevelInfo, "Nothing under the cursor")
		return
	}
	a.showTextModal(detailsPage, "Details", detailText(details), false)
}

func (a *App) detailsFor(area store.FocusArea) ([]detail, bool) {
	state := a.store.GetState()
	switch area {
	case store.FocusContexts:
		row, ok := a.selectedContextRow()
		return contextDetails(row.Name, state.Resources[row.Name]), ok
	case store.FocusNamespaces:
		if row, ok := a.selectedNamespaceRow(); ok {
			return namespaceDetails(row), true
		}
		if state.SelectedContext == "" {
			return nil, false
		}
		return contextDetails(state.SelectedContext, state.Resources[state.SelectedContext]), true
	case store.FocusServices:
		row, ok := a.selectedServiceRow()
		return serviceDetails(row), ok
	default:
		tunnel, ok := a.selectedTunnel()
		return tunnelDetails(tunnel), ok
	}
}

func contextDetails(name string, resources kube.ContextResources) []detail {
	discovery := "ok"
	if resources.Err != nil {
		discovery = "failed\n" + resources.Err.Error()
	}
	services := 0
	for _, list := range resources.Namespaces {
		services += len(list)
	}
	return []detail{
		{"Context", name},
		{"Discovery", discovery},
		{"Namespaces", strconv.Itoa(len(resources.Namespaces))},
		{"Services", strconv.Itoa(services)},
	}
}

func namespaceDetails(row namespaceRow) []detail {
	return []detail{
		{"Namespace", row.Name},
		{"Context", row.Context},
		{"Services", strconv.Itoa(len(row.Services))},
		{"Registrable", strconv.Itoa(registrableCount(row.Services))},
	}
}

func serviceDetails(row serviceRow) []detail {
	svc := row.Service
	eligibility := "registrable (port " + row.Port + ")"
	if row.Reason != "" {
		eligibility = row.Reason
	}
	registered := "no"
	if row.Owner != "" {
		registered = fmt.Sprintf("yes, context %s (http://%s)", row.Owner, row.DNSName())
	}
	return []detail{
		{"Service", svc.Name},
		{"Namespace", svc.Namespace},
		{"Context", row.Context},
		{"ClusterIP", svc.ClusterIP},
		{"Type", svc.Type},
		{"Ports", portLines(svc.Ports)},
		{"Selector", selectorText(svc.Selector)},
		{"Eligibility", eligibility},
		{"Registered", registered},
	}
}

func portLines(ports []kube.ServicePort) string {
	if len(ports) == 0 {
		return "none"
	}
	lines := make([]string, 0, len(ports))
	for _, port := range ports {
		name := port.Name
		if name == "" {
			name = "(unnamed)"
		}
		target := port.TargetPortName
		if target == "" {
			target = strconv.Itoa(int(port.TargetPort))
		}
		protocol := port.Protocol
		if protocol == "" {
			protocol = "TCP"
		}
		lines = append(lines, fmt.Sprintf("%s: port %d, targetPort %s, %s", name, port.Port, target, protocol))
	}
	return strings.Join(lines, "\n")
}

func selectorText(selector map[string]string) string {
	if len(selector) == 0 {
		return "none"
	}
	pairs := make([]string, 0, len(selector))
	for _, key := range slices.Sorted(maps.Keys(selector)) {
		pairs = append(pairs, key+"="+selector[key])
	}
	return strings.Join(pairs, ", ")
}

func tunnelDetails(tunnel dns.DNSTunnel) []detail {
	return []detail{
		{"URL", tunnelURL(tunnel)},
		{"Context", tunnel.Context},
		{"Namespace", tunnel.Namespace},
		{"Pod", tunnel.Pod},
		{"Local", fmt.Sprintf("127.0.0.1:%d", tunnel.LocalPort)},
		{"Pod port", strconv.Itoa(int(tunnel.RemotePort))},
	}
}
