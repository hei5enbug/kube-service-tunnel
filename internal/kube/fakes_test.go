package kube

import (
	"context"
	"fmt"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type fakePodClient struct {
	pods  []Pod
	err   error
	calls int
}

func (f *fakePodClient) ListPods(ctx context.Context, namespace, contextName string) ([]Pod, error) {
	return f.pods, f.err
}

func (f *fakePodClient) FindMatchingPods(ctx context.Context, namespace, contextName string, selector map[string]string) ([]Pod, error) {
	f.calls++
	return f.pods, f.err
}

type fakeServiceClient struct {
	byNamespace map[string][]Service
	errs        map[string]error
}

func (f *fakeServiceClient) ListServices(ctx context.Context, namespace, contextName string) ([]Service, error) {
	if err := f.errs[namespace]; err != nil {
		return nil, err
	}
	return f.byNamespace[namespace], nil
}

type fakeNamespaceClient struct {
	namespaces       []string
	err              error
	namespacesByCtx  map[string][]string
	namespaceErrsCtx map[string]error
}

func (f *fakeNamespaceClient) ListNamespaces(ctx context.Context, contextName string) ([]string, error) {
	if err := f.namespaceErrsCtx[contextName]; err != nil {
		return nil, err
	}
	if names, ok := f.namespacesByCtx[contextName]; ok {
		return names, nil
	}
	return f.namespaces, f.err
}

type fakeContextClient struct {
	contexts   []Context
	err        error
	current    string
	currentErr error
}

func (f *fakeContextClient) ListContexts(ctx context.Context) ([]Context, error) {
	return f.contexts, f.err
}

func (f *fakeContextClient) GetCurrentContext(ctx context.Context) (string, error) {
	return f.current, f.currentErr
}

type fakeStartedForward struct {
	namespace  string
	pod        string
	localPort  int32
	remotePort int32
}

type fakePortForwardClient struct {
	mu      sync.Mutex
	started []fakeStartedForward
	startFn func(pod string) error
}

func (f *fakePortForwardClient) StartPortForward(_ context.Context, contextName, namespace, pod string, localPort, remotePort int32, config *rest.Config, clientset kubernetes.Interface) (string, error) {
	if f.startFn != nil {
		if err := f.startFn(pod); err != nil {
			return "", err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, fakeStartedForward{namespace: namespace, pod: pod, localPort: localPort, remotePort: remotePort})
	return BuildPortForwardKey(contextName, namespace, pod, remotePort, localPort), nil
}

func (f *fakePortForwardClient) StopPortForward(key string) error {
	return fmt.Errorf("%w: %s", ErrPortForwardNotFound, key)
}

func (f *fakePortForwardClient) StopAllPortForwards() {}

func (f *fakePortForwardClient) SetLostHandler(handler func(key string, err error)) {}

func readyPod(name string, ports ...PodPort) Pod {
	return Pod{Name: name, Namespace: "app", Status: "Running", Ready: true, Ports: ports}
}

func newTestAdapter(pods *fakePodClient, services *fakeServiceClient, namespaces *fakeNamespaceClient, forwards *fakePortForwardClient) *kubeAdapter {
	return &kubeAdapter{
		podClient:         pods,
		serviceClient:     services,
		namespaceClient:   namespaces,
		portForwardClient: forwards,
		newClient: func(contextName string) (*rest.Config, kubernetes.Interface, error) {
			return &rest.Config{}, nil, nil
		},
	}
}
