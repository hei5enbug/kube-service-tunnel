package kube

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ContextResources holds the services discovered in one context. Namespaces keeps whatever was found even
// when Err reports partial failures.
type ContextResources struct {
	Namespaces map[string][]Service
	Err        error
}

func (m *kubeAdapter) FetchAllResources(ctx context.Context) (map[string]ContextResources, error) {
	contexts, err := m.ListContexts(ctx)
	if err != nil {
		return nil, err
	}

	fetches := make([]ContextResources, len(contexts))
	var wg sync.WaitGroup
	for i, c := range contexts {
		wg.Add(1)
		go func(index int, contextName string) {
			defer wg.Done()
			fetches[index] = m.FetchContextResources(ctx, contextName)
		}(i, c.Name)
	}
	wg.Wait()

	resources := make(map[string]ContextResources, len(contexts))
	for i, c := range contexts {
		resources[c.Name] = fetches[i]
	}
	return resources, nil
}

func (m *kubeAdapter) FetchContextResources(ctx context.Context, contextName string) ContextResources {
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()

	result := ContextResources{Namespaces: make(map[string][]Service)}
	var errs []error

	namespaces, err := m.ListNamespaces(ctx, contextName)
	if err != nil {
		result.Err = fmt.Errorf("context %s: %w", contextName, err)
		return result
	}

	for _, ns := range namespaces {
		if isSystemNamespace(ns) {
			continue
		}
		services, err := m.ListServices(ctx, ns, contextName)
		if err != nil {
			errs = append(errs, fmt.Errorf("context %s namespace %s: %w", contextName, ns, err))
			continue
		}
		if len(services) > 0 {
			result.Namespaces[ns] = services
		}
	}

	result.Err = errors.Join(errs...)
	return result
}
