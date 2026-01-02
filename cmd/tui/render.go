package tui

import "github.com/byoungmin/kube-service-tunnel/cmd/tui/store"

type stateChange struct {
	resources bool
	tunnels   bool
	chrome    bool
	loading   bool
}

func diffState(prev, next store.State) stateChange {
	selection := prev.SelectedContext != next.SelectedContext || prev.SelectedNamespace != next.SelectedNamespace
	return stateChange{
		resources: selection || prev.ResourcesRevision != next.ResourcesRevision,
		tunnels:   prev.TunnelsRevision != next.TunnelsRevision,
		chrome:    selection || prev.Status.ID != next.Status.ID || prev.Unread != next.Unread || prev.Focus != next.Focus,
		loading:   prev.IsLoading != next.IsLoading || prev.LoadingLabel != next.LoadingLabel,
	}
}

func (c stateChange) any() bool {
	return c.resources || c.tunnels || c.chrome || c.loading
}

// subscribe redraws from the store. The listener runs on the store's drain goroutine, so it may queue UI
// updates; the queued renders read the latest state, which keeps them idempotent.
func (a *App) subscribe() {
	var prev store.State
	a.store.Subscribe(func(next store.State) {
		change := diffState(prev, next)
		prev = next
		if !change.any() {
			return
		}
		a.app.QueueUpdateDraw(func() { a.applyChange(change) })
	})
}

func (a *App) applyChange(change stateChange) {
	if change.resources {
		a.renderResourcePanes()
	}
	if change.tunnels {
		a.refreshTunnelViews()
	}
	if change.resources || change.tunnels || change.chrome {
		a.renderChrome()
	}
	if change.loading {
		a.syncLoading()
	}
}

func (a *App) renderResourcePanes() {
	a.renderContexts()
	a.renderNamespaces()
	a.renderServices()
}
