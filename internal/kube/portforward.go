package kube

import (
	"fmt"
	"io"
	"net"
	"net/http"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

type PortForwardClientInterface interface {
	StartPortForward(contextName, namespace, pod string, localPort, remotePort int32, config *rest.Config, clientset kubernetes.Interface) error
	StopPortForward(key string) error
	GetActiveForwards() map[string]*PortForward
	StopAllPortForwards()
}

type portForwardClient struct {
	forwards map[string]*PortForward
}

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

func NewPortForwardClient() PortForwardClientInterface {
	return &portForwardClient{
		forwards: make(map[string]*PortForward),
	}
}

func (p *portForwardClient) StartPortForward(contextName, namespace, pod string, localPort, remotePort int32, config *rest.Config, clientset kubernetes.Interface) error {
	key := fmt.Sprintf("%s:%s:%s:%d", contextName, namespace, pod, remotePort)

	if _, exists := p.forwards[key]; exists {
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

	go startPortForwardGoroutine(config, clientset, namespace, pod, localPort, remotePort, stopCh, readyCh, errorCh)

	select {
	case <-readyCh:
		return nil
	case err := <-errorCh:
		delete(p.forwards, key)
		return err
	}
}

func (p *portForwardClient) StopPortForward(key string) error {
	forward, exists := p.forwards[key]
	if !exists {
		return fmt.Errorf("port forward not found: %s", key)
	}

	close(forward.StopCh)
	delete(p.forwards, key)
	return nil
}

func (p *portForwardClient) GetActiveForwards() map[string]*PortForward {
	result := make(map[string]*PortForward)
	for k, v := range p.forwards {
		result[k] = v
	}
	return result
}

func (p *portForwardClient) StopAllPortForwards() {
	for _, forward := range p.forwards {
		close(forward.StopCh)
	}
	p.forwards = make(map[string]*PortForward)
}

func startPortForwardGoroutine(config *rest.Config, clientset kubernetes.Interface, namespace, pod string, localPort, remotePort int32, stopCh, readyCh chan struct{}, errorCh chan error) {
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
}

func findAvailablePort(startPort int32, usedPorts map[int32]bool) (int32, error) {
	if startPort < 40000 {
		startPort = 40000
	}

	for port := startPort; port < 65535; port++ {
		if usedPorts[port] {
			continue
		}

		addr, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			continue
		}
		addr.Close()
		return port, nil
	}

	return 0, fmt.Errorf("no available port found starting from %d", startPort)
}
