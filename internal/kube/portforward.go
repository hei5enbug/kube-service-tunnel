package kube

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
)

const portForwardReadyTimeout = 30 * time.Second

var ErrPortForwardNotFound = errors.New("port forward not found")

type PortForwardClientInterface interface {
	StartPortForward(ctx context.Context, contextName, namespace, pod string, localPort, remotePort int32, config *rest.Config, clientset kubernetes.Interface) (forwardKey string, err error)
	StopPortForward(key string) error
	StopAllPortForwards()
	SetLostHandler(handler func(key string, err error))
}

type portForwardClient struct {
	forwards     map[string]*PortForward
	lostHandler  func(key string, err error)
	run          forwardRunner
	readyTimeout time.Duration
	mu           sync.Mutex
}

type PortForward struct {
	Key        string
	Context    string
	Namespace  string
	Pod        string
	LocalPort  int32
	RemotePort int32

	stopCh   chan struct{}
	stopOnce sync.Once
	errCh    chan error
	doneCh   chan struct{}
}

func (f *PortForward) stop() {
	f.stopOnce.Do(func() { close(f.stopCh) })
}

func NewPortForwardClient() PortForwardClientInterface {
	return newPortForwardClient(runPortForward, portForwardReadyTimeout)
}

func newPortForwardClient(run forwardRunner, readyTimeout time.Duration) *portForwardClient {
	return &portForwardClient{
		forwards:     make(map[string]*PortForward),
		run:          run,
		readyTimeout: readyTimeout,
	}
}

func BuildPortForwardKey(contextName, namespace, pod string, remotePort, localPort int32) string {
	return fmt.Sprintf("%s:%s:%s:%d:%d", contextName, namespace, pod, remotePort, localPort)
}

func (p *portForwardClient) SetLostHandler(handler func(key string, err error)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lostHandler = handler
}

func (p *portForwardClient) StartPortForward(ctx context.Context, contextName, namespace, pod string, localPort, remotePort int32, config *rest.Config, clientset kubernetes.Interface) (string, error) {
	key := BuildPortForwardKey(contextName, namespace, pod, remotePort, localPort)

	forward := &PortForward{
		Key:        key,
		Context:    contextName,
		Namespace:  namespace,
		Pod:        pod,
		LocalPort:  localPort,
		RemotePort: remotePort,
		stopCh:     make(chan struct{}),
		errCh:      make(chan error, 1),
		doneCh:     make(chan struct{}),
	}

	if err := p.add(forward); err != nil {
		return "", err
	}

	readyCh := make(chan struct{})
	request := forwardRequest{config: config, clientset: clientset, namespace: namespace, pod: pod, localPort: localPort, remotePort: remotePort}
	go p.runForward(forward, request, readyCh)

	if err := waitForForwardReady(ctx, readyCh, forward.errCh, forward.doneCh, p.readyTimeout); err != nil {
		forward.stop()
		p.removeIfCurrent(forward)
		return "", err
	}

	go p.watchForward(forward)
	return key, nil
}

func (p *portForwardClient) add(forward *PortForward) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.forwards[forward.Key]; exists {
		return fmt.Errorf("port forward already exists: %s", forward.Key)
	}
	p.forwards[forward.Key] = forward
	return nil
}

func (p *portForwardClient) removeIfCurrent(forward *PortForward) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.forwards[forward.Key] != forward {
		return false
	}
	delete(p.forwards, forward.Key)
	return true
}

func (p *portForwardClient) runForward(forward *PortForward, request forwardRequest, readyCh chan struct{}) {
	defer close(forward.doneCh)
	forward.errCh <- p.run(request, forward.stopCh, readyCh)
}

func (p *portForwardClient) watchForward(forward *PortForward) {
	<-forward.doneCh

	var exitErr error
	select {
	case exitErr = <-forward.errCh:
	default:
	}

	if !p.removeIfCurrent(forward) {
		return
	}

	p.mu.Lock()
	handler := p.lostHandler
	p.mu.Unlock()
	if handler == nil {
		return
	}

	if exitErr == nil {
		exitErr = portforward.ErrLostConnectionToPod
	}
	handler(forward.Key, exitErr)
}

func (p *portForwardClient) StopPortForward(key string) error {
	p.mu.Lock()
	forward, exists := p.forwards[key]
	if !exists {
		p.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrPortForwardNotFound, key)
	}
	delete(p.forwards, key)
	p.mu.Unlock()

	forward.stop()
	return nil
}

func (p *portForwardClient) StopAllPortForwards() {
	p.mu.Lock()
	forwards := make([]*PortForward, 0, len(p.forwards))
	for _, forward := range p.forwards {
		forwards = append(forwards, forward)
	}
	p.forwards = make(map[string]*PortForward)
	p.mu.Unlock()

	for _, forward := range forwards {
		forward.stop()
	}
}
