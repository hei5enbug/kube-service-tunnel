package store

import (
	"reflect"
	"slices"
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

type delivery struct {
	state     State
	listeners []func(State)
}

type Store struct {
	mutex     sync.RWMutex
	state     State
	listeners []func(State)

	queueMutex sync.Mutex
	pending    []delivery
	draining   bool
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
	defer store.mutex.Unlock()

	if !updateFn(&store.state) {
		return
	}

	store.enqueue(delivery{
		state:     copyState(store.state),
		listeners: slices.Clone(store.listeners),
	})
}

func copyState(state State) State {
	stateCopy := state
	stateCopy.Contexts = append([]kube.Context(nil), state.Contexts...)
	stateCopy.Namespaces = append([]string(nil), state.Namespaces...)
	stateCopy.Services = append([]kube.Service(nil), state.Services...)
	return stateCopy
}

// enqueue must be called with the state mutex held so that queue order equals commit order.
func (store *Store) enqueue(item delivery) {
	store.queueMutex.Lock()
	defer store.queueMutex.Unlock()

	store.pending = append(store.pending, item)
	if store.draining {
		return
	}
	store.draining = true
	go store.drain()
}

func (store *Store) drain() {
	for {
		item, ok := store.dequeue()
		if !ok {
			return
		}
		for _, listener := range item.listeners {
			listener(item.state)
		}
	}
}

func (store *Store) dequeue() (delivery, bool) {
	store.queueMutex.Lock()
	defer store.queueMutex.Unlock()

	if len(store.pending) == 0 {
		store.pending = nil
		store.draining = false
		return delivery{}, false
	}
	item := store.pending[0]
	store.pending = store.pending[1:]
	return item, true
}

// TryBeginLoading sets IsLoading and reports true only when it was not already set.
func (store *Store) TryBeginLoading() bool {
	began := false
	store.setState(func(state *State) bool {
		if state.IsLoading {
			return false
		}
		state.IsLoading = true
		began = true
		return true
	})
	return began
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
