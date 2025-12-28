package store

import (
	"reflect"
	"sort"
	"sync"

	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

type FocusArea string

const (
	FocusContexts   FocusArea = "contexts"
	FocusNamespaces FocusArea = "namespaces"
	FocusServices   FocusArea = "services"
	FocusTunnels    FocusArea = "tunnels"
)

type State struct {
	ResourceMap       map[string]map[string][]kube.Service // {Context: {Namespace: [Services]}}
	Contexts          []kube.Context
	Namespaces        []string
	Services          []kube.Service
	SelectedContext   string
	SelectedNamespace string
	IsLoading         bool
	Message           string
	Focus             FocusArea
}

type Store struct {
	mutex     sync.RWMutex
	state     State
	listeners []func(State)
}

func NewStore() *Store {
	return &Store{
		state: State{},
	}
}

func (store *Store) GetState() State {
	store.mutex.RLock()
	defer store.mutex.RUnlock()
	return store.state
}

func (store *Store) Subscribe(listener func(State)) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.listeners = append(store.listeners, listener)
}

func (store *Store) setState(updateFn func(*State) bool) {
	store.mutex.Lock()
	changed := updateFn(&store.state)
	if !changed {
		store.mutex.Unlock()
		return
	}

	stateCopy := store.state
	stateCopy.Contexts = make([]kube.Context, len(store.state.Contexts))
	copy(stateCopy.Contexts, store.state.Contexts)
	stateCopy.Namespaces = make([]string, len(store.state.Namespaces))
	copy(stateCopy.Namespaces, store.state.Namespaces)
	stateCopy.Services = make([]kube.Service, len(store.state.Services))
	copy(stateCopy.Services, store.state.Services)

	currentListeners := make([]func(State), len(store.listeners))
	copy(currentListeners, store.listeners)
	store.mutex.Unlock()

	for _, listener := range currentListeners {
		listener(stateCopy)
	}
}

func (store *Store) SetLoading(loading bool) {
	store.setState(func(state *State) bool {
		if state.IsLoading == loading {
			return false
		}
		state.IsLoading = loading
		return true
	})
}

func (store *Store) SetMessage(message string) {
	store.setState(func(state *State) bool {
		if state.Message == message {
			return false
		}
		state.Message = message
		return true
	})
}

func (store *Store) SetContexts(contexts []kube.Context) {
	store.setState(func(state *State) bool {
		state.Contexts = contexts
		return true
	})
}

func (store *Store) SetAllResources(resourceMap map[string]map[string][]kube.Service) {
	store.setState(func(state *State) bool {
		state.ResourceMap = resourceMap

		contexts := make([]kube.Context, 0, len(resourceMap))
		for name := range resourceMap {
			contexts = append(contexts, kube.Context{Name: name})
		}
		sort.Slice(contexts, func(i, j int) bool {
			return contexts[i].Name < contexts[j].Name
		})
		state.Contexts = contexts

		if len(contexts) > 0 {
			if state.SelectedContext == "" {
				state.SelectedContext = contexts[0].Name
			}
			if ctxMap, ok := resourceMap[state.SelectedContext]; ok {
				var namespaces []string
				for ns := range ctxMap {
					namespaces = append(namespaces, ns)
				}
				sort.Strings(namespaces)
				state.Namespaces = namespaces

				if state.SelectedNamespace == "" && len(namespaces) > 0 {
					state.SelectedNamespace = namespaces[0]
				}
				if state.SelectedNamespace != "" {
					state.Services = ctxMap[state.SelectedNamespace]
				}
			}
		}
		return true
	})
}

func (store *Store) SetSelectedContext(contextName string) {
	store.setState(func(state *State) bool {
		if state.SelectedContext == contextName {
			return false
		}
		state.SelectedContext = contextName
		state.SelectedNamespace = ""
		state.Namespaces = nil
		state.Services = nil
		return true
	})
}

func (store *Store) SetSelectedContextWithResources(contextName string) {
	store.setState(func(state *State) bool {
		if state.SelectedContext == contextName {
			return false
		}

		state.SelectedContext = contextName

		ctxMap, ok := state.ResourceMap[contextName]
		if !ok {
			state.SelectedNamespace = ""
			state.Namespaces = nil
			state.Services = nil
			return true
		}

		var namespaces []string
		for ns := range ctxMap {
			namespaces = append(namespaces, ns)
		}
		sort.Strings(namespaces)
		state.Namespaces = namespaces

		if len(namespaces) > 0 {
			state.SelectedNamespace = namespaces[0]
			state.Services = ctxMap[state.SelectedNamespace]
		} else {
			state.SelectedNamespace = ""
			state.Services = nil
		}

		return true
	})
}

func (store *Store) SetNamespaces(namespaces []string) {
	store.setState(func(state *State) bool {
		if reflect.DeepEqual(state.Namespaces, namespaces) {
			return false
		}
		state.Namespaces = namespaces
		return true
	})
}

func (store *Store) SetSelectedNamespace(namespace string) {
	store.setState(func(state *State) bool {
		if state.SelectedNamespace == namespace {
			return false
		}
		state.SelectedNamespace = namespace

		if ctxMap, ok := state.ResourceMap[state.SelectedContext]; ok {
			state.Services = ctxMap[namespace]
		}

		return true
	})
}

func (store *Store) SetServices(services []kube.Service) {
	store.setState(func(state *State) bool {
		if reflect.DeepEqual(state.Services, services) {
			return false
		}
		state.Services = services
		return true
	})
}

func (store *Store) SetFocus(focus FocusArea) {
	store.setState(func(state *State) bool {
		if state.Focus == focus {
			return false
		}
		state.Focus = focus
		return true
	})
}
