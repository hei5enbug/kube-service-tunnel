package tui

import (
	"fmt"
	"strconv"

	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

const (
	noNamespaceText    = "Select a\nnamespace."
	noServicesText     = "No services."
	registeredSymbol   = "●"
	unregistrableSym   = "✗"
	serviceSeparators  = 3
	serviceMarkerWidth = 1
	minPortWidth       = 4
	minNameWidth       = 8
	minNoteWidth       = 9
)

type serviceRow struct {
	Context string
	Service kube.Service
	Port    string
	Reason  string
	Owner   string
}

func (row serviceRow) DNSName() string {
	return kube.BuildServiceDNS(row.Service.Name, row.Service.Namespace)
}

func (row serviceRow) Key() string {
	return row.Service.Namespace + "/" + row.Service.Name
}

func buildServiceRows(state store.State, owners map[string]string) []serviceRow {
	rows := make([]serviceRow, 0, len(state.Services))
	for i := range state.Services {
		svc := state.Services[i]
		port, reason := kube.Eligibility(&svc)
		row := serviceRow{Context: state.SelectedContext, Service: svc, Port: "-", Reason: reason}
		if port != nil {
			row.Port = strconv.Itoa(int(port.Port))
		}
		row.Owner = owners[row.DNSName()]
		rows = append(rows, row)
	}
	return rows
}

// serviceMarker returns the marker, note and dimming of a row. A registered name wins over eligibility because
// the URL is live.
func serviceMarker(row serviceRow) (string, string, bool) {
	switch {
	case row.Owner != "" && row.Owner != row.Context:
		return registeredSymbol, "in " + row.Owner, false
	case row.Owner != "":
		return registeredSymbol, "", false
	case row.Reason != "":
		return unregistrableSym, row.Reason, true
	default:
		return " ", "", false
	}
}

func (a *App) renderServices() {
	p := a.pane(store.FocusServices)
	state := a.store.GetState()
	all := buildServiceRows(state, tunnelOwners(a.tunnels))
	rows := filterRows(all, func(row serviceRow) bool { return matchFilter(p.filter, row.Service.Name) })
	a.serviceRows = rows

	base := paneHeading(p.area)
	if state.SelectedNamespace != "" {
		base += ": " + state.SelectedNamespace
	}
	empty := noServicesText
	if state.SelectedNamespace == "" {
		empty = noNamespaceText
	}
	view := serviceView(rows, a.layout.innerWidth(p.area))
	view.title = paneTitle(base, p.filter, len(rows), len(all))
	view.empty = emptyText(len(all), empty)
	a.drawTable(p, view)
}

func serviceView(rows []serviceRow, inner int) tableView {
	nameWidth, portWidth, noteWidth := serviceColumns(rows, inner)
	view := tableView{header: []viewCell{
		{text: " ", kind: cellHeader},
		{text: fit("NAME", nameWidth), kind: cellHeader, expand: true},
		{text: fit("PORT", portWidth), kind: cellHeader},
		{text: fit("NOTE", noteWidth), kind: cellHeader},
	}}
	for _, row := range rows {
		symbol, note, dim := serviceMarker(row)
		kind := cellNormal
		if dim {
			kind = cellDim
		}
		view.rows = append(view.rows, []viewCell{
			{text: symbol, kind: kind},
			{text: fit(row.Service.Name, nameWidth), kind: kind},
			{text: fit(row.Port, portWidth), kind: kind},
			{text: fit(note, noteWidth), kind: kind},
		})
		view.keys = append(view.keys, row.Key())
	}
	return view
}

// serviceColumns gives NAME its width first, then NOTE; PORT keeps its natural width.
func serviceColumns(rows []serviceRow, inner int) (int, int, int) {
	nameNatural, portWidth, noteNatural := displayWidth("NAME"), minPortWidth, displayWidth("NOTE")
	for _, row := range rows {
		_, note, _ := serviceMarker(row)
		nameNatural = max(nameNatural, displayWidth(row.Service.Name))
		portWidth = max(portWidth, displayWidth(row.Port))
		noteNatural = max(noteNatural, displayWidth(note))
	}
	available := inner - serviceSeparators - serviceMarkerWidth - portWidth
	widths := allocateWidths(available, []column{
		{natural: nameNatural, minimum: minNameWidth},
		{natural: noteNatural, minimum: minNoteWidth},
	})
	return widths[0], portWidth, widths[1]
}

func (a *App) selectedServiceRow() (serviceRow, bool) {
	index, ok := a.pane(store.FocusServices).selectedIndex()
	if !ok || index >= len(a.serviceRows) {
		return serviceRow{}, false
	}
	return a.serviceRows[index], true
}

func (a *App) registerService() {
	if a.store.GetState().SelectedNamespace == "" {
		a.store.Notify(store.LevelInfo, "Please select a namespace first")
		return
	}
	row, ok := a.selectedServiceRow()
	if !ok {
		a.store.Notify(store.LevelInfo, "No service under the cursor")
		return
	}
	dnsName := row.DNSName()
	switch {
	case row.Owner != "":
		a.store.Notify(store.LevelWarning, fmt.Sprintf("http://%s is already registered (context %s)", dnsName, row.Owner))
		return
	case row.Reason != "":
		a.store.Notify(store.LevelWarning, fmt.Sprintf("%s cannot be registered: %s", row.Key(), row.Reason))
		return
	}
	if !a.store.TryBeginLoading(fmt.Sprintf("Registering %s in %s", dnsName, row.Context)) {
		return
	}
	go a.runRegister(row.Context, row.Service.Name, row.Service.Namespace, dnsName)
}

func (a *App) runRegister(contextName, serviceName, namespace, dnsName string) {
	defer a.store.SetLoading(false)
	err := a.manager.RegisterDNSTunnel(a.ctx, contextName, serviceName, namespace)
	a.store.TunnelsChanged()
	if err != nil {
		a.store.Notify(store.LevelError, fmt.Sprintf("Registering %s failed: %v", dnsName, err))
		return
	}
	a.store.Notify(store.LevelSuccess, "Port forwarding started: http://"+dnsName)
}
