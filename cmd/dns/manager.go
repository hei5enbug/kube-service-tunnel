package dns

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/byoungmin/kube-service-tunnel/internal/host"
	"github.com/byoungmin/kube-service-tunnel/internal/k8s"
	"github.com/byoungmin/kube-service-tunnel/internal/portforward"
	"github.com/byoungmin/kube-service-tunnel/internal/proxy"
	"github.com/byoungmin/kube-service-tunnel/internal/utils"
	"k8s.io/client-go/kubernetes"
)

type DNSTunnel struct {
	Context   string
	Namespace string
	DNSURL    string
}

type DnsManager struct {
	kubeconfigPath     string
	contextClient      k8s.ContextClientInterface
	namespaceClient    k8s.NamespaceInterface
	serviceClient      k8s.ServiceInterface
	hostsFileManager   host.HostsFileManagerInterface
	portForwardManager *portforward.PortForwardManager
	proxyServer        *proxy.ProxyServer
	podClient          k8s.PodInterface
	selectedContext    string
	selectedNamespace  string
	contexts           []k8s.Context
	namespaces         []string
	services           []k8s.Service
	dnsTunnels         []DNSTunnel
}

func NewDnsManager(kubeconfigPath string) (*DnsManager, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	contextClient, err := k8s.NewContextClient(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("create context client: %w", err)
	}

	namespaceClient, err := k8s.NewNamespaceClient(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("create namespace client: %w", err)
	}

	serviceClient, err := k8s.NewServiceClient(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("create service client: %w", err)
	}

	podClient, err := k8s.NewPodClient(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("create pod client: %w", err)
	}

	contexts, err := contextClient.ListContexts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list contexts: %w", err)
	}
	sort.Slice(contexts, func(i, j int) bool {
		return contexts[i].Name < contexts[j].Name
	})

	currentContext, err := contextClient.GetCurrentContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current context: %w", err)
	}

	dnsManager := &DnsManager{
		kubeconfigPath:     kubeconfigPath,
		contextClient:      contextClient,
		namespaceClient:    namespaceClient,
		serviceClient:      serviceClient,
		hostsFileManager:   host.NewHostsFileManager(),
		portForwardManager: portforward.NewPortForwardManager(kubeconfigPath),
		proxyServer:        proxy.NewProxyServer(),
		podClient:          podClient,
		selectedContext:    currentContext,
		contexts:           contexts,
		namespaces:         []string{},
		services:           []k8s.Service{},
		dnsTunnels:         []DNSTunnel{},
	}

	if err := dnsManager.RefreshDNSEntries(); err != nil {
		return nil, fmt.Errorf("refresh DNS entries: %w", err)
	}

	return dnsManager, nil
}

func (m *DnsManager) GetContexts() []k8s.Context {
	return m.contexts
}

func (m *DnsManager) GetSelectedContext() string {
	return m.selectedContext
}

func (m *DnsManager) GetNamespaces() []string {
	return m.namespaces
}

func (m *DnsManager) GetSelectedNamespace() string {
	return m.selectedNamespace
}

func (m *DnsManager) GetServices() []k8s.Service {
	return m.services
}

func (m *DnsManager) SetSelectedContext(contextName string) error {
	m.selectedContext = contextName
	m.selectedNamespace = ""
	return m.RefreshNamespaces()
}

func (m *DnsManager) SetSelectedNamespace(namespace string) error {
	m.selectedNamespace = namespace
	return m.RefreshServices()
}

func (m *DnsManager) isSystemNamespace(namespace string) bool {
	systemNamespaces := []string{"kube-system", "kube-public", "kube-node-lease"}
	for _, sysNs := range systemNamespaces {
		if namespace == sysNs {
			return true
		}
	}
	return false
}

func (m *DnsManager) RefreshNamespaces() error {
	if m.selectedContext == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	allNamespaces, err := m.namespaceClient.ListNamespaces(ctx, m.selectedContext)
	if err != nil {
		return fmt.Errorf("list namespaces: %w", err)
	}

	var namespacesWithServices []string
	for _, ns := range allNamespaces {
		if m.isSystemNamespace(ns) {
			continue
		}
		services, err := m.serviceClient.ListServices(ctx, ns, m.selectedContext)
		if err != nil {
			continue
		}
		if len(services) > 0 {
			namespacesWithServices = append(namespacesWithServices, ns)
		}
	}

	m.namespaces = namespacesWithServices
	return nil
}

func (m *DnsManager) RefreshServices() error {
	if m.selectedContext == "" || m.selectedNamespace == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	services, err := m.serviceClient.ListServices(ctx, m.selectedNamespace, m.selectedContext)
	if err != nil {
		return fmt.Errorf("list services: %w", err)
	}

	m.services = services
	return nil
}

func (m *DnsManager) GetRegisteredDNSEntries() []DNSTunnel {
	return m.dnsTunnels
}

func (m *DnsManager) RefreshDNSEntries() error {
	entries, err := m.hostsFileManager.ReadExistingEntries()
	if err != nil {
		return fmt.Errorf("read existing entries: %w", err)
	}

	var tunnels []DNSTunnel
	for dnsName := range entries {
		var namespace string
		if strings.Contains(dnsName, ":") {
			parts := strings.Split(dnsName, ":")
			if len(parts) >= 2 {
				namespacePart := strings.Split(parts[1], ".")
				if len(namespacePart) >= 2 {
					namespace = namespacePart[1]
				}
			}
		} else {
			dnsParts := strings.Split(dnsName, ".")
			if len(dnsParts) >= 2 {
				namespace = dnsParts[1]
			}
		}
		if namespace != "" {
			context := m.selectedContext
			if context == "" {
				context = "unknown"
			}
			tunnels = append(tunnels, DNSTunnel{
				Context:   context,
				Namespace: namespace,
				DNSURL:    dnsName,
			})
		}
	}

	m.dnsTunnels = tunnels
	return nil
}

func (m *DnsManager) RegisterAllServicesForContext(contextName string) error {
	if contextName == "" {
		return fmt.Errorf("context name is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	allNamespaces, err := m.namespaceClient.ListNamespaces(ctx, contextName)
	if err != nil {
		return fmt.Errorf("list namespaces: %w", err)
	}

	config, err := k8s.LoadKubeconfigWithContext(m.kubeconfigPath, contextName)
	if err != nil {
		return fmt.Errorf("load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create kubernetes client: %w", err)
	}

	if !m.proxyServer.IsRunning() {
		if err := m.proxyServer.Start(80); err != nil {
			return fmt.Errorf("start proxy server: %w", err)
		}
	}

	existingEntries, err := m.hostsFileManager.ReadExistingEntries()
	if err != nil {
		existingEntries = make(map[string]string)
	}

	var registeredCount int
	var errorCount int
	for _, ns := range allNamespaces {
		if m.isSystemNamespace(ns) {
			continue
		}
		services, err := m.serviceClient.ListServices(ctx, ns, contextName)
		if err != nil {
			errorCount++
			continue
		}
		for _, svc := range services {
			if svc.ClusterIP == "" {
				continue
			}

			var httpPort *k8s.ServicePort
			for i := range svc.Ports {
				if utils.IsHTTPPort(svc.Ports[i].Port) {
					httpPort = &svc.Ports[i]
					break
				}
			}

			if httpPort == nil {
				errorCount++
				continue
			}

			pods, err := m.podClient.FindMatchingPods(ctx, ns, contextName, svc.Selector)
			if err != nil {
				errorCount++
				continue
			}

			if len(pods) == 0 {
				errorCount++
				continue
			}

			pod := pods[0]
			var podPort int32
			if httpPort.TargetPort > 0 {
				podPort = httpPort.TargetPort
			} else {
				for _, p := range pod.Ports {
					if p.Name == httpPort.Name || p.ContainerPort == httpPort.Port {
						podPort = p.ContainerPort
						break
					}
				}
				if podPort == 0 && len(pod.Ports) > 0 {
					podPort = pod.Ports[0].ContainerPort
				}
			}

			if podPort == 0 {
				errorCount++
				continue
			}

			activeForwards := m.portForwardManager.GetActiveForwards()
			usedPorts := make(map[int32]bool)
			for _, forward := range activeForwards {
				usedPorts[forward.LocalPort] = true
			}

			localPort, err := utils.FindAvailablePort(40000, usedPorts)
			if err != nil {
				errorCount++
				continue
			}

			err = m.portForwardManager.StartPortForward(contextName, ns, pod.Name, localPort, podPort, config, clientset)
			if err != nil {
				errorCount++
				continue
			}

			serviceHost := fmt.Sprintf("%s.%s", svc.Name, ns)
			serviceDNS := serviceHost
			if httpPort.Port != 80 {
				serviceDNS = fmt.Sprintf("%s:%d.%s", svc.Name, httpPort.Port, ns)
			}
			m.proxyServer.AddRoute(serviceDNS, localPort)

			existingEntries[serviceDNS] = "127.0.0.1"

			registeredCount++
		}
	}

	if registeredCount > 0 {
		if err := m.hostsFileManager.WriteEntries(existingEntries); err != nil {
			return fmt.Errorf("write hosts file: %w", err)
		}
	}

	if err := m.RefreshDNSEntries(); err != nil {
		return fmt.Errorf("refresh DNS entries: %w", err)
	}

	if registeredCount == 0 {
		return fmt.Errorf("no services found to register")
	}

	return nil
}

func (m *DnsManager) RegisterService(serviceName, namespace string) error {
	if serviceName == "" || namespace == "" {
		return fmt.Errorf("service name and namespace are required")
	}

	services := m.GetServices()
	var targetService *k8s.Service
	for _, svc := range services {
		if svc.Name == serviceName && svc.Namespace == namespace {
			if svc.ClusterIP == "" {
				return fmt.Errorf("service %s/%s has no ClusterIP", namespace, serviceName)
			}
			targetService = &svc
			break
		}
	}

	if targetService == nil {
		return fmt.Errorf("service %s/%s not found in current namespace", namespace, serviceName)
	}

	dnsName := fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, namespace)
	
	existingEntries, err := m.hostsFileManager.ReadExistingEntries()
	if err != nil {
		existingEntries = make(map[string]string)
	}
	
	existingEntries[dnsName] = targetService.ClusterIP
	
	if err := m.hostsFileManager.WriteEntries(existingEntries); err != nil {
		return fmt.Errorf("write service to hosts file: %w", err)
	}

	if err := m.RefreshDNSEntries(); err != nil {
		return fmt.Errorf("refresh DNS entries: %w", err)
	}

	return nil
}

func (m *DnsManager) RegisterServicePortForward(serviceName, namespace string) error {
	if serviceName == "" || namespace == "" {
		return fmt.Errorf("service name and namespace are required")
	}

	services := m.GetServices()
	var targetService *k8s.Service
	for _, svc := range services {
		if svc.Name == serviceName && svc.Namespace == namespace {
			if svc.ClusterIP == "" {
				return fmt.Errorf("service %s/%s has no ClusterIP", namespace, serviceName)
			}
			targetService = &svc
			break
		}
	}

	if targetService == nil {
		return fmt.Errorf("service %s/%s not found in current namespace", namespace, serviceName)
	}

	var httpPort *k8s.ServicePort
	for i := range targetService.Ports {
		if utils.IsHTTPPort(targetService.Ports[i].Port) {
			httpPort = &targetService.Ports[i]
			break
		}
	}

	if httpPort == nil {
		return fmt.Errorf("no HTTP port found for service %s/%s", namespace, serviceName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pods, err := m.podClient.FindMatchingPods(ctx, namespace, m.selectedContext, targetService.Selector)
	if err != nil {
		return fmt.Errorf("find matching pods: %w", err)
	}

	if len(pods) == 0 {
		return fmt.Errorf("no matching pods found for service %s/%s", namespace, serviceName)
	}

	pod := pods[0]
	var podPort int32
	if httpPort.TargetPort > 0 {
		podPort = httpPort.TargetPort
	} else {
		for _, p := range pod.Ports {
			if p.Name == httpPort.Name || p.ContainerPort == httpPort.Port {
				podPort = p.ContainerPort
				break
			}
		}
		if podPort == 0 && len(pod.Ports) > 0 {
			podPort = pod.Ports[0].ContainerPort
		}
	}

	if podPort == 0 {
		return fmt.Errorf("could not determine pod port for service %s/%s", namespace, serviceName)
	}

	activeForwards := m.portForwardManager.GetActiveForwards()
	usedPorts := make(map[int32]bool)
	for _, forward := range activeForwards {
		usedPorts[forward.LocalPort] = true
	}

	localPort, err := utils.FindAvailablePort(40000, usedPorts)
	if err != nil {
		return fmt.Errorf("find available port: %w", err)
	}

	config, err := k8s.LoadKubeconfigWithContext(m.kubeconfigPath, m.selectedContext)
	if err != nil {
		return fmt.Errorf("load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create kubernetes client: %w", err)
	}

	err = m.portForwardManager.StartPortForward(m.selectedContext, namespace, pod.Name, localPort, podPort, config, clientset)
	if err != nil {
		return fmt.Errorf("start port forward: %w", err)
	}

	serviceHost := fmt.Sprintf("%s.%s", serviceName, namespace)
	serviceDNS := serviceHost
	if httpPort.Port != 80 {
		serviceDNS = fmt.Sprintf("%s:%d.%s", serviceName, httpPort.Port, namespace)
	}

	if !m.proxyServer.IsRunning() {
		if err := m.proxyServer.Start(80); err != nil {
			return fmt.Errorf("start proxy server: %w", err)
		}
	}

	m.proxyServer.AddRoute(serviceDNS, localPort)

	existingEntries, err := m.hostsFileManager.ReadExistingEntries()
	if err != nil {
		existingEntries = make(map[string]string)
	}

	existingEntries[serviceDNS] = "127.0.0.1"

	if err := m.hostsFileManager.WriteEntries(existingEntries); err != nil {
		return fmt.Errorf("write service to hosts file: %w", err)
	}

	if err := m.RefreshDNSEntries(); err != nil {
		return fmt.Errorf("refresh DNS entries: %w", err)
	}

	return nil
}

func (m *DnsManager) UnregisterServicePortForward(dnsURL string) error {
	if dnsURL == "" {
		return fmt.Errorf("DNS URL is required")
	}

	var targetLocalPort int32
	var forwardKey string

	if m.proxyServer != nil && m.proxyServer.IsRunning() {
		routes := m.proxyServer.GetRoutes()
		if port, exists := routes[dnsURL]; exists {
			targetLocalPort = port
		}
	}

	if targetLocalPort == 0 {
		activeForwards := m.portForwardManager.GetActiveForwards()
		for key, forward := range activeForwards {
			if strings.Contains(key, dnsURL) {
				targetLocalPort = forward.LocalPort
				forwardKey = key
				break
			}
		}
		if targetLocalPort == 0 {
			return fmt.Errorf("port forward not found for DNS URL: %s", dnsURL)
		}
	} else {
		activeForwards := m.portForwardManager.GetActiveForwards()
		for key, forward := range activeForwards {
			if forward.LocalPort == targetLocalPort {
				forwardKey = key
				break
			}
		}
	}

	if forwardKey == "" {
		return fmt.Errorf("port forward not found for local port: %d", targetLocalPort)
	}

	if err := m.portForwardManager.StopPortForward(forwardKey); err != nil {
		return fmt.Errorf("stop port forward: %w", err)
	}

	if m.proxyServer != nil && m.proxyServer.IsRunning() {
		m.proxyServer.RemoveRoute(dnsURL)
	}

	existingEntries, err := m.hostsFileManager.ReadExistingEntries()
	if err != nil {
		return fmt.Errorf("read existing entries: %w", err)
	}

	delete(existingEntries, dnsURL)

	if err := m.hostsFileManager.WriteEntries(existingEntries); err != nil {
		return fmt.Errorf("write hosts file: %w", err)
	}

	if err := m.RefreshDNSEntries(); err != nil {
		return fmt.Errorf("refresh DNS entries: %w", err)
	}

	return nil
}

func (m *DnsManager) CleanupHostsFile() error {
	if m.hostsFileManager != nil {
		if err := m.hostsFileManager.ClearAllEntries(); err != nil {
			return fmt.Errorf("clear hosts file entries: %w", err)
		}
	}
	return nil
}

func (m *DnsManager) Cleanup() error {
	if m.portForwardManager != nil {
		m.portForwardManager.StopAll()
	}

	if m.proxyServer != nil && m.proxyServer.IsRunning() {
		if err := m.proxyServer.Stop(); err != nil {
			return fmt.Errorf("stop proxy server: %w", err)
		}
	}

	return m.CleanupHostsFile()
}

func FindContextIndex(contexts []k8s.Context, selectedContext string) int {
	for i, ctx := range contexts {
		if ctx.Name == selectedContext {
			return i
		}
	}
	return -1
}

