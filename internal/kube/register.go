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

func (m *kubeAdapter) RegisterAllServicesForContext(
	ctx context.Context,
	contextName string,
	namespaces []string,
	usedPorts map[int32]bool,
	registeredDNS map[string]bool,
) (RegisterAllResult, error) {
	config, clientset, err := m.newClient(contextName)
	if err != nil {
		return RegisterAllResult{}, err
	}

	namespaces, err = m.resolveNamespaces(ctx, contextName, namespaces)
	if err != nil {
		return RegisterAllResult{}, err
	}

	var result RegisterAllResult
	for _, ns := range namespaces {
		nsResult := m.registerNamespace(ctx, contextName, ns, usedPorts, registeredDNS, config, clientset)
		result.Tunnels = append(result.Tunnels, nsResult.Tunnels...)
		result.Failures = append(result.Failures, nsResult.Failures...)
	}

	if len(result.Tunnels) == 0 && len(result.Failures) == 0 {
		return result, ErrNoEligibleServices
	}
	return result, nil
}

func (m *kubeAdapter) resolveNamespaces(ctx context.Context, contextName string, namespaces []string) ([]string, error) {
	if len(namespaces) > 0 {
		return namespaces, nil
	}

	listCtx, cancel := withAPITimeout(ctx)
	defer cancel()

	listed, err := m.namespaceClient.ListNonSystemNamespaces(listCtx, contextName)
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}
	return listed, nil
}

func (m *kubeAdapter) registerNamespace(
	ctx context.Context,
	contextName string,
	namespace string,
	usedPorts map[int32]bool,
	registeredDNS map[string]bool,
	config *rest.Config,
	clientset kubernetes.Interface,
) RegisterAllResult {
	listCtx, cancel := withAPITimeout(ctx)
	services, err := m.ListServices(listCtx, namespace, contextName)
	cancel()
	if err != nil {
		return RegisterAllResult{Failures: []ServiceRegistrationFailure{{Namespace: namespace, Reason: err.Error()}}}
	}

	var result RegisterAllResult
	for _, svc := range services {
		if PickHTTPPort(&svc) == nil {
			continue
		}

		if registeredDNS[BuildServiceDNS(svc.Name, svc.Namespace)] {
			result.Failures = append(result.Failures, failureFor(svc, "already registered"))
			continue
		}

		tunnel, err := m.registerService(ctx, contextName, svc, usedPorts, config, clientset)
		if err != nil {
			result.Failures = append(result.Failures, failureFor(svc, err.Error()))
			continue
		}
		result.Tunnels = append(result.Tunnels, tunnel)
	}
	return result
}

func failureFor(svc Service, reason string) ServiceRegistrationFailure {
	return ServiceRegistrationFailure{Namespace: svc.Namespace, Service: svc.Name, Reason: reason}
}

func (m *kubeAdapter) registerService(
	ctx context.Context,
	contextName string,
	svc Service,
	usedPorts map[int32]bool,
	config *rest.Config,
	clientset kubernetes.Interface,
) (ServiceTunnel, error) {
	httpPort := PickHTTPPort(&svc)
	if httpPort == nil {
		return ServiceTunnel{}, errNoSupportedHTTPPort
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
