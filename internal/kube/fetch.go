package kube

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type contextFetch struct {
	services map[string][]Service
	errs     []error
}

func (m *kubeAdapter) FetchAllResources(ctx context.Context) (map[string]map[string][]Service, error) {
	contexts, err := m.ListContexts(ctx)
	if err != nil {
		return nil, err
	}

	fetches := make([]contextFetch, len(contexts))
	var wg sync.WaitGroup
	for i, c := range contexts {
		wg.Add(1)
		go func(index int, contextName string) {
			defer wg.Done()
			fetches[index] = m.fetchContext(ctx, contextName)
		}(i, c.Name)
	}
	wg.Wait()

	resourceMap := make(map[string]map[string][]Service, len(contexts))
	var errs []error
	for i, c := range contexts {
		resourceMap[c.Name] = fetches[i].services
		errs = append(errs, fetches[i].errs...)
	}

	return resourceMap, errors.Join(errs...)
}

func (m *kubeAdapter) fetchContext(ctx context.Context, contextName string) contextFetch {
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()

	result := contextFetch{services: make(map[string][]Service)}

	namespaces, err := m.ListNamespaces(ctx, contextName)
	if err != nil {
		result.errs = append(result.errs, fmt.Errorf("context %s: %w", contextName, err))
		return result
	}

	for _, ns := range namespaces {
		if isSystemNamespace(ns) {
			continue
		}
		services, err := m.ListServices(ctx, ns, contextName)
		if err != nil {
			result.errs = append(result.errs, fmt.Errorf("context %s namespace %s: %w", contextName, ns, err))
			continue
		}
		if len(services) > 0 {
			result.services[ns] = services
		}
	}

	return result
}
