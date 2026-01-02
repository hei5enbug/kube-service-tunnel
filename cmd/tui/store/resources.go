package store

import (
	"maps"
	"slices"

	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

// SetAllResources replaces every context. The active context stays when it still exists; otherwise preferred
// becomes active when present, and the first context when not.
func (store *Store) SetAllResources(resources map[string]kube.ContextResources, preferred string) {
	store.setState(func(state *State) bool {
		state.Resources = resources
		state.Contexts = slices.Sorted(maps.Keys(resources))
		state.ResourcesRevision++

		active := state.SelectedContext
		if _, ok := resources[active]; !ok {
			active = pickContext(state.Contexts, preferred)
		}
		activate(state, active, state.SelectedNamespace)
		return true
	})
}

// SetContextResources replaces one context. When it is active, the selected namespace stays if it still exists.
func (store *Store) SetContextResources(contextName string, resources kube.ContextResources) {
	store.setState(func(state *State) bool {
		next := make(map[string]kube.ContextResources, len(state.Resources)+1)
		maps.Copy(next, state.Resources)
		next[contextName] = resources
		state.Resources = next
		state.Contexts = slices.Sorted(maps.Keys(next))
		state.ResourcesRevision++

		if state.SelectedContext == contextName {
			activate(state, contextName, state.SelectedNamespace)
		}
		return true
	})
}

// ActivateContext makes contextName active with its first namespace selected.
func (store *Store) ActivateContext(contextName string) {
	store.setState(func(state *State) bool {
		if state.SelectedContext == contextName {
			return false
		}
		activate(state, contextName, "")
		return true
	})
}

func (store *Store) ActivateNamespace(namespace string) {
	store.setState(func(state *State) bool {
		if state.SelectedNamespace == namespace {
			return false
		}
		state.SelectedNamespace = namespace
		state.Services = state.Resources[state.SelectedContext].Namespaces[namespace]
		return true
	})
}

func pickContext(contexts []string, preferred string) string {
	if slices.Contains(contexts, preferred) {
		return preferred
	}
	if len(contexts) > 0 {
		return contexts[0]
	}
	return ""
}

func activate(state *State, contextName, namespace string) {
	namespaces := slices.Sorted(maps.Keys(state.Resources[contextName].Namespaces))
	if !slices.Contains(namespaces, namespace) {
		namespace = ""
		if len(namespaces) > 0 {
			namespace = namespaces[0]
		}
	}
	state.SelectedContext = contextName
	state.Namespaces = namespaces
	state.SelectedNamespace = namespace
	state.Services = state.Resources[contextName].Namespaces[namespace]
}
