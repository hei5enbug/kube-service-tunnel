package store

import (
	"slices"
	"sync"
	"time"

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
	Resources         map[string]kube.ContextResources
	Contexts          []string
	Namespaces        []string
	Services          []kube.Service
	SelectedContext   string
	SelectedNamespace string
	ResourcesRevision uint64
	TunnelsRevision   uint64
	IsLoading         bool
	LoadingLabel      string
	Status            Event
	History           []Event
	Unread            int
	Focus             FocusArea
	// TunnelSettings is replaced, never changed in place, so a state copy may share it.
	TunnelSettings map[string]TunnelSetting
}

type delivery struct {
	state     State
	listeners []func(State)
}

type Option func(*Store)

// WithStatusTTL sets how long info and success events stay on the status line. Zero disables expiry.
func WithStatusTTL(ttl time.Duration) Option {
	return func(store *Store) {
		store.statusTTL = ttl
	}
}

type Store struct {
	mutex       sync.RWMutex
	state       State
	listeners   []func(State)
	lastEventID uint64
	statusTTL   time.Duration
	now         func() time.Time

	queueMutex sync.Mutex
	pending    []delivery
	draining   bool
}

func NewStore(options ...Option) *Store {
	store := &Store{statusTTL: DefaultStatusTTL, now: time.Now}
	for _, option := range options {
		option(store)
	}
	return store
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
	stateCopy.Contexts = slices.Clone(state.Contexts)
	stateCopy.Namespaces = slices.Clone(state.Namespaces)
	stateCopy.Services = slices.Clone(state.Services)
	stateCopy.History = slices.Clone(state.History)
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

// TryBeginLoading sets IsLoading with the given label and reports true only when it was not already set.
func (store *Store) TryBeginLoading(label string) bool {
	began := false
	store.setState(func(state *State) bool {
		if state.IsLoading {
			return false
		}
		state.IsLoading = true
		state.LoadingLabel = label
		began = true
		return true
	})
	return began
}

// SetLoadingLabel replaces the label of the running operation. It does nothing when nothing is loading.
func (store *Store) SetLoadingLabel(label string) {
	store.setState(func(state *State) bool {
		if !state.IsLoading || state.LoadingLabel == label {
			return false
		}
		state.LoadingLabel = label
		return true
	})
}

func (store *Store) SetLoading(loading bool) {
	store.setState(func(state *State) bool {
		if state.IsLoading == loading {
			return false
		}
		state.IsLoading = loading
		state.LoadingLabel = ""
		return true
	})
}

// TunnelsChanged tells listeners that the tunnel list of the manager may have changed.
func (store *Store) TunnelsChanged() {
	store.setState(func(state *State) bool {
		state.TunnelsRevision++
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
