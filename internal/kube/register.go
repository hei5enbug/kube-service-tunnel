package kube

import (
	"context"
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func withAPITimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, apiCallTimeout)
}

func (m *kubeAdapter) RegisterServicePortForward(ctx context.Context, contextName, serviceName, namespace string, usedPorts map[int32]bool) (ServiceTunnel, error) {
	listCtx, cancel := withAPITimeout(ctx)
	services, err := m.ListServices(listCtx, namespace, contextName)
	cancel()
	if err != nil {
		return ServiceTunnel{}, fmt.Errorf("list services: %w", err)
	}

	svc, found := findService(services, serviceName, namespace)
	if !found {
		return ServiceTunnel{}, fmt.Errorf("service %s/%s not found (headless, NodePort and LoadBalancer services are excluded)", namespace, serviceName)
	}

	config, clientset, err := m.newClient(contextName)
	if err != nil {
		return ServiceTunnel{}, err
	}

	tunnel, err := m.registerService(ctx, contextName, svc, usedPorts, config, clientset)
	if err != nil {
		return ServiceTunnel{}, fmt.Errorf("service %s/%s: %w", namespace, serviceName, err)
	}
	return tunnel, nil
}

func findService(services []Service, name, namespace string) (Service, bool) {
	for _, svc := range services {
		if svc.Name == name && svc.Namespace == namespace {
			return svc, true
		}
	}
	return Service{}, false
}

// RegisterServicesForContext registers the given services in order. Services are listed freshly once per
// distinct namespace. progress, when non-nil, is called synchronously on the caller's goroutine before each
// target (Done = targets finished) and once after the last one (Done == Total, empty Current); it is not
// called after the context is canceled.
func (m *kubeAdapter) RegisterServicesForContext(
	ctx context.Context,
	contextName string,
	targets []ServiceRef,
	usedPorts map[int32]bool,
	owners map[string]string,
	progress func(RegistrationProgress),
) (RegisterAllResult, error) {
	if len(targets) == 0 {
		return RegisterAllResult{}, ErrNoEligibleServices
	}

	config, clientset, err := m.newClient(contextName)
	if err != nil {
		return RegisterAllResult{}, err
	}

	run := &bulkRun{
		adapter:     m,
		contextName: contextName,
		usedPorts:   usedPorts,
		owners:      copyOwners(owners),
		config:      config,
		clientset:   clientset,
		listings:    make(map[string]namespaceListing),
	}

	var result RegisterAllResult
	for i, target := range targets {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if progress != nil {
			progress(RegistrationProgress{Done: i, Total: len(targets), Current: BuildServiceDNS(target.Name, target.Namespace)})
		}
		run.process(ctx, target, &result)
	}
	if progress != nil {
		progress(RegistrationProgress{Done: len(targets), Total: len(targets)})
	}
	return result, nil
}

func copyOwners(owners map[string]string) map[string]string {
	copied := make(map[string]string, len(owners))
	for dns, owner := range owners {
		copied[dns] = owner
	}
	return copied
}

type namespaceListing struct {
	services []Service
	err      error
}

type bulkRun struct {
	adapter     *kubeAdapter
	contextName string
	usedPorts   map[int32]bool
	owners      map[string]string
	config      *rest.Config
	clientset   kubernetes.Interface
	listings    map[string]namespaceListing
}

func (r *bulkRun) listing(ctx context.Context, namespace string) namespaceListing {
	if cached, ok := r.listings[namespace]; ok {
		return cached
	}
	listCtx, cancel := withAPITimeout(ctx)
	services, err := r.adapter.ListServices(listCtx, namespace, r.contextName)
	cancel()
	listing := namespaceListing{services: services, err: err}
	r.listings[namespace] = listing
	return listing
}

func (r *bulkRun) process(ctx context.Context, target ServiceRef, result *RegisterAllResult) {
	fail := func(reason string) ServiceRegistrationFailure {
		return ServiceRegistrationFailure{Namespace: target.Namespace, Service: target.Name, Reason: reason}
	}

	listing := r.listing(ctx, target.Namespace)
	if listing.err != nil {
		result.Failures = append(result.Failures, fail(listing.err.Error()))
		return
	}

	svc, found := findService(listing.services, target.Name, target.Namespace)
	if !found {
		result.Failures = append(result.Failures, fail("not found"))
		return
	}

	if _, reason := Eligibility(&svc); reason != "" {
		result.Ineligible = append(result.Ineligible, fail(reason))
		return
	}

	dns := BuildServiceDNS(svc.Name, svc.Namespace)
	if owner := r.owners[dns]; owner != "" {
		result.Skipped = append(result.Skipped, fail(fmt.Sprintf("already registered (context %s)", owner)))
		return
	}

	tunnel, err := r.adapter.registerService(ctx, r.contextName, svc, r.usedPorts, r.config, r.clientset)
	if err != nil {
		result.Failures = append(result.Failures, fail(err.Error()))
		return
	}
	r.owners[dns] = r.contextName
	result.Tunnels = append(result.Tunnels, tunnel)
}

func (m *kubeAdapter) registerService(
	ctx context.Context,
	contextName string,
	svc Service,
	usedPorts map[int32]bool,
	config *rest.Config,
	clientset kubernetes.Interface,
) (ServiceTunnel, error) {
	httpPort, reason := Eligibility(&svc)
	if reason != "" {
		return ServiceTunnel{}, eligibilityError(reason)
	}

	podCtx, cancel := withAPITimeout(ctx)
	pod, podPort, err := FindPodAndPortForService(podCtx, m.podClient, svc.Namespace, contextName, svc.Selector, httpPort)
	cancel()
	if err != nil {
		return ServiceTunnel{}, err
	}

	localPort, err := findAvailablePort(minLocalPort, usedPorts)
	if err != nil {
		return ServiceTunnel{}, fmt.Errorf("find available port: %w", err)
	}

	forwardKey, err := m.portForwardClient.StartPortForward(ctx, contextName, svc.Namespace, pod.Name, localPort, podPort, config, clientset)
	if err != nil {
		return ServiceTunnel{}, fmt.Errorf("start port forward: %w", err)
	}
	if usedPorts != nil {
		usedPorts[localPort] = true
	}

	return ServiceTunnel{
		Context:    contextName,
		Namespace:  svc.Namespace,
		DNSURL:     BuildServiceDNS(svc.Name, svc.Namespace),
		Pod:        pod.Name,
		LocalPort:  localPort,
		RemotePort: podPort,
		ForwardKey: forwardKey,
	}, nil
}
