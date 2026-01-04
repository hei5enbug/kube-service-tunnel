package tui

import (
	"fmt"
	"strconv"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

const (
	noNamespaceText    = "Select a\nnamespace."
	noServicesText     = "No services."
	registeredSymbol   = "●"
	nameTakenSymbol    = "○"
	unregistrableSym   = "✗"
	serviceSeparators  = 3
	serviceMarkerWidth = 1
	minPortWidth       = 4
	minNameWidth       = 8
	minNoteWidth       = 9
)

// serviceRow is one service with what registering it would meet. Registered wins over NameUser, and both
// win over Reason, in the order the Services pane checks them.
type serviceRow struct {
	Context    string
	Service    kube.Service
	Port       string
	Reason     string
	Target     kube.ServiceRef
	Tunnel     dns.DNSTunnel
	Registered bool
	// NameUser is the context of another tunnel that uses the hostname of Target.
	NameUser string
}

func newServiceRow(contextName string, svc kube.Service, settings map[string]store.TunnelSetting, index tunnelIndex) serviceRow {
	port, reason := kube.Eligibility(&svc)
	key := store.SettingKey(contextName, svc.Namespace, svc.Name)
	row := serviceRow{Context: contextName, Service: svc, Port: "-", Reason: reason, Target: serviceTarget(svc, settings[key])}
	if port != nil {
		row.Port = strconv.Itoa(int(port.Port))
	}
	if tunnel, ok := index.byService[key]; ok {
		row.Tunnel, row.Registered = tunnel, true
	} else if holder, ok := index.byHost[row.Hostname()]; ok {
		row.NameUser = holder.Context
	}
	return row
}

// Hostname is the remembered hostname of the service, or its default hostname.
func (row serviceRow) Hostname() string {
	return row.Target.EffectiveHostname()
}

func (row serviceRow) DefaultHostname() string {
	return kube.BuildServiceDNS(row.Service.Name, row.Service.Namespace)
}

func (row serviceRow) Key() string {
	return row.Service.Namespace + "/" + row.Service.Name
}

func (row serviceRow) Label() string {
	return serviceLabel(row.Context, row.Service.Namespace, row.Service.Name)
}

func buildServiceRows(state store.State, index tunnelIndex) []serviceRow {
	rows := make([]serviceRow, 0, len(state.Services))
	for _, svc := range state.Services {
		rows = append(rows, newServiceRow(state.SelectedContext, svc, state.TunnelSettings, index))
	}
	return rows
}

// serviceMarker returns the marker, note and dimming of a row. Registration wins over eligibility because
// the URL is live.
func serviceMarker(row serviceRow) (string, string, bool) {
	switch {
	case row.Registered && row.Tunnel.DNSURL != row.DefaultHostname():
		return registeredSymbol, "→ " + row.Tunnel.DNSURL, false
	case row.Registered:
		return registeredSymbol, "", false
	case row.NameUser != "":
		return nameTakenSymbol, "in " + row.NameUser, false
	case row.Reason != "":
		return unregistrableSym, row.Reason, true
	default:
		return " ", "", false
	}
}

func (a *App) renderServices() {
	p := a.pane(store.FocusServices)
	state := a.store.GetState()
	all := buildServiceRows(state, indexTunnels(a.tunnels))
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
	if refusal := registerRefusal(row); refusal != "" {
		a.store.Notify(store.LevelWarning, refusal)
		return
	}
	if !a.store.TryBeginLoading(fmt.Sprintf("Registering %s in %s", row.Hostname(), row.Context)) {
		return
	}
	go a.runRegister(row.Context, row.Target)
}

// registerRefusal explains why Enter does not register the row, or returns "" when it may.
func registerRefusal(row serviceRow) string {
	switch {
	case row.Registered:
		return fmt.Sprintf("%s is already registered as %s", row.Label(), tunnelURL(row.Tunnel))
	case row.NameUser != "":
		return fmt.Sprintf("http://%s is used by context %s. Rename that tunnel with e in Tunnels first.", row.Hostname(), row.NameUser)
	case row.Reason != "":
		return fmt.Sprintf("%s cannot be registered: %s", row.Key(), row.Reason)
	default:
		return ""
	}
}

func (a *App) runRegister(contextName string, target kube.ServiceRef) {
	defer a.store.SetLoading(false)
	tunnel, err := a.manager.RegisterDNSTunnel(a.ctx, contextName, target)
	a.store.TunnelsChanged()
	if err != nil {
		a.store.Notify(store.LevelError, fmt.Sprintf("Registering %s failed: %v", target.EffectiveHostname(), err))
		return
	}
	text := "Port forwarding started: " + tunnelURL(tunnel)
	if fallback, busy := portFallback(tunnel); busy {
		a.store.Notify(store.LevelWarning, text+" · "+fallback)
		return
	}
	a.store.Notify(store.LevelSuccess, text)
}
