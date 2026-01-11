package kube

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
)

const (
	portForwardReadyTimeout = 30 * time.Second
	portForwardStopTimeout  = 5 * time.Second
)

type expectedPodUIDKey struct{}

// withExpectedPodUID carries the UID of the pod a forward is meant for, so the pod monitor never adopts
// another pod with the same name as its baseline.
func withExpectedPodUID(ctx context.Context, uid types.UID) context.Context {
	return context.WithValue(ctx, expectedPodUIDKey{}, uid)
}

func expectedPodUID(ctx context.Context) types.UID {
	uid, _ := ctx.Value(expectedPodUIDKey{}).(types.UID)
	return uid
}

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
	detectLoss   podLossDetector
	stopTimeout  time.Duration
	readyTimeout time.Duration
	// background tracks the forward watchers and pod monitors, including the lost handler call they make.
	// Only tests wait on it, to know that nothing more can be reported.
	background sync.WaitGroup
	mu         sync.Mutex
}

type PortForward struct {
	Key        string
	Context    string
	Namespace  string
	Pod        string
	LocalPort  int32
	RemotePort int32

	stopCh        chan struct{}
	stopOnce      sync.Once
	monitorCtx    context.Context
	cancelMonitor context.CancelFunc
	errCh         chan error
	doneCh        chan struct{}
}

func (f *PortForward) stop() {
	f.stopOnce.Do(func() {
		close(f.stopCh)
		f.cancelMonitor()
	})
}

func NewPortForwardClient() PortForwardClientInterface {
	return newPortForwardClient(runPortForward, portForwardReadyTimeout)
}

func newPortForwardClient(run forwardRunner, readyTimeout time.Duration) *portForwardClient {
	return &portForwardClient{
		forwards:     make(map[string]*PortForward),
		run:          run,
		detectLoss:   watchPodLoss,
		stopTimeout:  portForwardStopTimeout,
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
	expectedUID := expectedPodUID(ctx)

	monitorCtx, cancelMonitor := context.WithCancel(context.Background())
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

		monitorCtx:    monitorCtx,
		cancelMonitor: cancelMonitor,
	}

	if err := p.add(forward); err != nil {
		cancelMonitor()
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

	p.goTracked(func() { p.watchForward(forward) })
	if clientset != nil {
		p.goTracked(func() { p.monitorPod(forward, clientset, expectedUID) })
	}
	return key, nil
}

func (p *portForwardClient) goTracked(fn func()) {
	p.background.Add(1)
	go func() {
		defer p.background.Done()
		fn()
	}()
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

	if exitErr == nil {
		exitErr = portforward.ErrLostConnectionToPod
	}
	p.markLost(forward, exitErr)
}

// monitorPod reports the forward as lost when its pod goes away, until the forward stops for any reason.
func (p *portForwardClient) monitorPod(forward *PortForward, clientset kubernetes.Interface, expected types.UID) {
	if err := p.detectLoss(forward.monitorCtx, clientset, forward.Namespace, forward.Pod, expected); err != nil {
		p.markLost(forward, err)
	}
}

// markLost removes the forward when it is still the current one, stops it, waits for its local port to be
// released, and then calls the lost handler. Only the caller that removes the forward reports the loss.
func (p *portForwardClient) markLost(forward *PortForward, lostErr error) {
	if !p.removeIfCurrent(forward) {
		return
	}
	forward.stop()

	select {
	case <-forward.doneCh:
	case <-time.After(p.stopTimeout):
	}

	p.mu.Lock()
	handler := p.lostHandler
	p.mu.Unlock()
	if handler != nil {
		handler(forward.Key, lostErr)
	}
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
