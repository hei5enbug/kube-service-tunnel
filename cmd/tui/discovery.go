package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

const loadingContextsLabel = "Loading contexts…"

// start claims the loading state before the event loop runs, so input is gated until discovery ends.
func (a *App) start() {
	if !a.store.TryBeginLoading(loadingContextsLabel) {
		return
	}
	a.background(a.loadAll)
}

func (a *App) loadAll() {
	defer a.store.SetLoading(false)
	current, currentErr := a.kubeAdapter.CurrentContext(a.ctx)
	fetched, err := a.kubeAdapter.FetchAllResources(a.ctx)
	if err != nil {
		a.store.Notify(store.LevelError, "Cannot list contexts: "+err.Error())
		return
	}
	a.store.SetAllResources(fetched, current)
	a.store.TunnelsChanged()
	if currentErr != nil {
		a.store.Notify(store.LevelInfo, "Cannot read the current context, so the first context is active: "+currentErr.Error())
	}
	a.reportDiscovery(fetched)
}

func (a *App) reportDiscovery(fetched map[string]kube.ContextResources) {
	names := slices.Sorted(maps.Keys(fetched))
	if len(names) == 0 {
		a.store.Notify(store.LevelWarning, "No contexts found in the kubeconfig")
		return
	}
	var failures []string
	for _, name := range names {
		if err := fetched[name].Err; err != nil {
			failures = append(failures, fmt.Sprintf("Discovery failed for %s: %v", name, err))
		}
	}
	summary := "Loaded " + plural(len(names), "context")
	if len(failures) > 0 {
		a.store.Notify(store.LevelWarning, fmt.Sprintf("%s · %d failed\n%s", summary, len(failures), strings.Join(failures, "\n")))
		return
	}
	a.store.Notify(store.LevelSuccess, summary)
}

func (a *App) refreshActiveContext() {
	name := a.store.GetState().SelectedContext
	if name == "" {
		a.store.Notify(store.LevelInfo, "No active context to refresh")
		return
	}
	if !a.store.TryBeginLoading(fmt.Sprintf("Refreshing %s…", name)) {
		return
	}
	a.background(func() { a.runRefresh(name) })
}

func (a *App) runRefresh(name string) {
	defer a.store.SetLoading(false)
	resources := a.kubeAdapter.FetchContextResources(a.ctx, name)
	a.store.SetContextResources(name, resources)
	a.store.TunnelsChanged()
	if resources.Err != nil {
		a.store.Notify(store.LevelWarning, fmt.Sprintf("Refreshed %s with errors: %v", name, resources.Err))
		return
	}
	a.store.Notify(store.LevelSuccess, fmt.Sprintf("Refreshed %s · %s", name, plural(len(resources.Namespaces), "namespace")))
}
