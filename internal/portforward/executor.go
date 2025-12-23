package portforward

import (
	"fmt"
	"io"
	"net/http"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

func StartPortForwardGoroutine(config *rest.Config, clientset kubernetes.Interface, namespace, pod string, localPort, remotePort int32, stopCh, readyCh chan struct{}, errorCh chan error) {
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

