package display

import (
	"context"
	"fmt"
	"time"

	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

type UIRenderer struct {
	kubeAdapter       kube.KubeAdapterInterface
	selectedContext   string
	selectedNamespace string
	contexts          []kube.Context
	namespaces        []string
	services          []kube.Service
}

func NewUIRenderer(kubeconfigPath string) (*UIRenderer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	kubeAdapter, err := kube.NewKubeAdapter(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("create kube adapter: %w", err)
	}

	contexts, err := kubeAdapter.ListContexts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list contexts: %w", err)
	}

	var selectedContext string
	if len(contexts) > 0 {
		selectedContext = contexts[0].Name
	}

	renderer := &UIRenderer{
		kubeAdapter:       kubeAdapter,
		selectedContext:   selectedContext,
		selectedNamespace: "",
		contexts:          contexts,
		namespaces:        []string{},
		services:          []kube.Service{},
	}

	if err := renderer.RefreshNamespaces(); err != nil {
		return nil, fmt.Errorf("refresh namespaces: %w", err)
	}

	return renderer, nil
}

func (r *UIRenderer) GetContexts() []kube.Context {
	return r.contexts
}

func (r *UIRenderer) GetSelectedContext() string {
	return r.selectedContext
}

func (r *UIRenderer) GetNamespaces() []string {
	return r.namespaces
}

func (r *UIRenderer) GetSelectedNamespace() string {
	return r.selectedNamespace
}

func (r *UIRenderer) GetServices() []kube.Service {
	return r.services
}

func (r *UIRenderer) RefreshNamespaces() error {
	if r.selectedContext == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	allNamespaces, err := r.kubeAdapter.ListNamespaces(ctx, r.selectedContext)
	if err != nil {
		return fmt.Errorf("list namespaces: %w", err)
	}

	var namespacesWithServices []string
	for _, ns := range allNamespaces {
		if isSystemNamespace(ns) {
			continue
		}
		services, err := r.kubeAdapter.ListServices(ctx, ns, r.selectedContext)
		if err != nil {
			continue
		}
		if len(services) > 0 {
			namespacesWithServices = append(namespacesWithServices, ns)
		}
	}

	r.namespaces = namespacesWithServices
	return nil
}

func (r *UIRenderer) SetSelectedContext(contextName string) error {
	r.selectedContext = contextName
	r.selectedNamespace = ""
	r.services = nil
	return r.RefreshNamespaces()
}

func (r *UIRenderer) SetSelectedNamespace(namespace string) error {
	r.selectedNamespace = namespace

	if r.selectedContext == "" || r.selectedNamespace == "" {
		r.services = nil
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	services, err := r.kubeAdapter.ListServices(ctx, r.selectedNamespace, r.selectedContext)
	if err != nil {
		return fmt.Errorf("list services: %w", err)
	}

	r.services = services
	return nil
}

// FindContextIndex returns the index of the selected context in the list for UI highlighting.
func FindContextIndex(contexts []kube.Context, selectedContext string) int {
	for i, ctx := range contexts {
		if ctx.Name == selectedContext {
			return i
		}
	}
	return -1
}

func isSystemNamespace(namespace string) bool {
	systemNamespaces := []string{"kube-system", "kube-public", "kube-node-lease"}
	for _, sysNs := range systemNamespaces {
		if namespace == sysNs {
			return true
		}
	}
	return false
}
