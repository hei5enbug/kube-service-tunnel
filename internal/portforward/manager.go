package portforward

import (
	"fmt"
	"io"
	"net/http"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

type PortForward struct {
	Key        string
	Context    string
	Namespace  string
	Pod        string
	LocalPort  int32
	RemotePort int32
	StopCh     chan struct{}
	ReadyCh    chan struct{}
	ErrorCh    chan error
}

type PortForwardManager struct {
	forwards       map[string]*PortForward
	mu             sync.RWMutex
	kubeconfigPath string
	clientsets     map[string]kubernetes.Interface
	configs        map[string]*rest.Config
	muClients      sync.RWMutex
}

func (p *PortForwardManager) StartPortForward(contextName, namespace, pod string, localPort, remotePort int32, config *rest.Config, clientset kubernetes.Interface) error {
	key := fmt.Sprintf("%s:%s:%s:%d", contextName, namespace, pod, remotePort)

	p.mu.Lock()
	if _, exists := p.forwards[key]; exists {
		p.mu.Unlock()
		return fmt.Errorf("port forward already exists: %s", key)
	}

	stopCh := make(chan struct{}, 1)
	readyCh := make(chan struct{})
	errorCh := make(chan error, 1)

	forward := &PortForward{
		Key:        key,
		Context:    contextName,
		Namespace:  namespace,
		Pod:        pod,
		LocalPort:  localPort,
		RemotePort: remotePort,
		StopCh:     stopCh,
		ReadyCh:    readyCh,
		ErrorCh:    errorCh,
	}

	p.forwards[key] = forward
	p.mu.Unlock()

	p.muClients.Lock()
	p.clientsets[contextName] = clientset
	p.configs[contextName] = config
	p.muClients.Unlock()

	go func() {
		defer close(stopCh)
		defer close(readyCh)
		defer close(errorCh)

		reqURL := clientset.CoreV1().RESTClient().Post().
			Resource("pods").
			Namespace(namespace).
			Name(pod).
			SubResource("portforward").URL()

		transport, upgrader, err := spdy.RoundTripperFor(config)
		if err != nil {
			errorCh <- fmt.Errorf("create round tripper: %w", err)
			return
		}

		dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", reqURL)

		ports := []string{
			fmt.Sprintf("%d:%d", localPort, remotePort),
		}

		pf, err := portforward.New(dialer, ports, stopCh, readyCh, io.Discard, io.Discard)
		if err != nil {
			errorCh <- fmt.Errorf("create port forward: %w", err)
			return
		}

		err = pf.ForwardPorts()
		if err != nil {
			errorCh <- fmt.Errorf("forward ports: %w", err)
		}
	}()

	select {
	case <-readyCh:
		return nil
	case err := <-errorCh:
		p.mu.Lock()
		delete(p.forwards, key)
		p.mu.Unlock()
		return err
	}
}

func (p *PortForwardManager) StopPortForward(key string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	forward, exists := p.forwards[key]
	if !exists {
		return fmt.Errorf("port forward not found: %s", key)
	}

	close(forward.StopCh)
	delete(p.forwards, key)
	return nil
}

func (p *PortForwardManager) GetActiveForwards() map[string]*PortForward {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make(map[string]*PortForward)
	for k, v := range p.forwards {
		result[k] = v
	}
	return result
}

func (p *PortForwardManager) GetLocalPort(key string) (int32, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	forward, exists := p.forwards[key]
	if !exists {
		return 0, false
	}
	return forward.LocalPort, true
}

func (p *PortForwardManager) StopAll() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, forward := range p.forwards {
		close(forward.StopCh)
	}
	p.forwards = make(map[string]*PortForward)
}

