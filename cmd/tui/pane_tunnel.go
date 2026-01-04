package tui

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/clipboard"
)

const (
	noTunnelsText = "No tunnels yet.\nEnter on a service\nadds one."
	osc52Message  = "Copy request sent to the terminal (OSC 52). If nothing was copied, press i to see the URL."
	minURLWidth   = 12
	minCtxWidth   = 8
	minPodWidth   = 16
)

func tunnelURL(tunnel dns.DNSTunnel) string {
	return "http://" + tunnel.DNSURL
}

func sortedTunnels(tunnels []dns.DNSTunnel) []dns.DNSTunnel {
	sorted := slices.Clone(tunnels)
	slices.SortFunc(sorted, func(x, y dns.DNSTunnel) int { return cmp.Compare(x.DNSURL, y.DNSURL) })
	return sorted
}

// refreshTunnelViews reloads the tunnel list and re-renders everything that shows registration state.
func (a *App) refreshTunnelViews() {
	next := sortedTunnels(a.manager.GetAllDNSTunnels())
	a.followRenamedTunnel(next)
	a.tunnels = next
	a.renderServices()
	a.renderTunnels()
	a.renderHeader()
}

func (a *App) renderTunnels() {
	p := a.pane(store.FocusTunnels)
	rows := filterRows(a.tunnels, func(tunnel dns.DNSTunnel) bool {
		return matchFilter(p.filter, tunnelURL(tunnel), tunnel.Context)
	})
	a.tunnelRows = rows
	view := tunnelView(rows, a.layout.innerWidth(p.area), a.layout.paneWidth(p.area) >= podMinPaneWidth)
	view.title = paneTitle(paneHeading(p.area), p.filter, len(rows), len(a.tunnels))
	view.empty = emptyText(len(a.tunnels), noTunnelsText)
	a.drawTable(p, view)
}

func tunnelView(rows []dns.DNSTunnel, inner int, withPod bool) tableView {
	columns := []column{{natural: displayWidth("URL"), minimum: minURLWidth}, {natural: displayWidth("CTX"), minimum: minCtxWidth}}
	if withPod {
		columns = append(columns, column{natural: displayWidth("POD"), minimum: minPodWidth})
	}
	for _, row := range rows {
		columns[0].natural = max(columns[0].natural, displayWidth(tunnelURL(row)))
		columns[1].natural = max(columns[1].natural, displayWidth(row.Context))
		if withPod {
			columns[2].natural = max(columns[2].natural, displayWidth(row.Pod))
		}
	}
	widths := allocateWidths(inner-(len(columns)-1), columns)

	view := tableView{header: []viewCell{
		{text: fit("URL", widths[0]), kind: cellHeader, expand: true},
		{text: fit("CTX", widths[1]), kind: cellHeader},
	}}
	if withPod {
		view.header = append(view.header, viewCell{text: fit("POD", widths[2]), kind: cellHeader})
	}
	for _, row := range rows {
		cells := []viewCell{{text: fit(tunnelURL(row), widths[0])}, {text: fit(row.Context, widths[1])}}
		if withPod {
			cells = append(cells, viewCell{text: fit(row.Pod, widths[2])})
		}
		view.rows = append(view.rows, cells)
		view.keys = append(view.keys, row.DNSURL)
	}
	return view
}

// followRenamedTunnel keeps the Tunnels cursor on the tunnel under it when its DNS URL changed, matching the
// tunnel by its service. It clears the Tunnels filter when the new name no longer matches it, so the row stays
// visible.
func (a *App) followRenamedTunnel(next []dns.DNSTunnel) {
	current, ok := a.selectedTunnel()
	if !ok || slices.ContainsFunc(next, func(t dns.DNSTunnel) bool { return t.DNSURL == current.DNSURL }) {
		return
	}
	key := tunnelSettingKey(current)
	index := slices.IndexFunc(next, func(t dns.DNSTunnel) bool { return tunnelSettingKey(t) == key })
	if index < 0 {
		return
	}
	p := a.pane(store.FocusTunnels)
	p.renameKey(current.DNSURL, next[index].DNSURL)
	if !matchFilter(p.filter, tunnelURL(next[index]), next[index].Context) {
		p.filter = ""
	}
}

func (a *App) selectedTunnel() (dns.DNSTunnel, bool) {
	index, ok := a.pane(store.FocusTunnels).selectedIndex()
	if !ok || index >= len(a.tunnelRows) {
		return dns.DNSTunnel{}, false
	}
	return a.tunnelRows[index], true
}

func (a *App) deleteTunnel() {
	tunnel, ok := a.selectedTunnel()
	if !ok {
		a.store.Notify(store.LevelInfo, "No tunnel under the cursor")
		return
	}
	url := tunnelURL(tunnel)
	if !a.store.TryBeginLoading("Removing " + url + "…") {
		return
	}
	go a.runDelete(tunnel.DNSURL, url)
}

func (a *App) runDelete(dnsURL, url string) {
	defer a.store.SetLoading(false)
	err := a.manager.UnregisterDNSTunnel(dnsURL)
	a.store.TunnelsChanged()
	if err != nil {
		a.store.Notify(store.LevelError, fmt.Sprintf("Removing %s failed: %v", url, err))
		return
	}
	a.store.Notify(store.LevelSuccess, "Tunnel removed: "+url)
}

func (a *App) copyTunnelURL() {
	tunnel, ok := a.selectedTunnel()
	if !ok {
		a.store.Notify(store.LevelInfo, "No tunnel under the cursor")
		return
	}
	go a.copyText(tunnelURL(tunnel))
}

// copyText runs off the UI goroutine. It falls back to OSC 52 when the system clipboard fails.
func (a *App) copyText(text string) {
	err := a.copier.Copy(a.ctx, text)
	if err == nil {
		a.store.Notify(store.LevelSuccess, "Copied "+text)
		return
	}
	if !errors.Is(err, clipboard.ErrUnsupported) {
		a.store.Notify(store.LevelInfo, "System clipboard failed: "+err.Error())
	}
	if screen := a.screenRef.get(); screen != nil {
		screen.SetClipboard([]byte(text))
	}
	a.store.Notify(store.LevelInfo, osc52Message)
}

// onTunnelLost runs on a manager goroutine, so it only calls non-blocking store setters.
func (a *App) onTunnelLost(tunnel dns.DNSTunnel, err error) {
	a.store.TunnelsChanged()
	a.store.Notify(store.LevelWarning, fmt.Sprintf(
		"Tunnel lost: %s (context %s): %v. Press Enter on the service to register it again.",
		tunnelURL(tunnel), tunnel.Context, err))
}
