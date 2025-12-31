package kube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

const forwardBindAddress = "127.0.0.1"

var (
	errForwardExitedEarly = errors.New("port forward exited before becoming ready")
	errForwardNotReady    = errors.New("port forward not ready before timeout")
)

type forwardRequest struct {
	config     *rest.Config
	clientset  kubernetes.Interface
	namespace  string
	pod        string
	localPort  int32
	remotePort int32
}

type forwardRunner func(request forwardRequest, stopCh <-chan struct{}, readyCh chan struct{}) error

func runPortForward(request forwardRequest, stopCh <-chan struct{}, readyCh chan struct{}) error {
	reqURL := request.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(request.namespace).
		Name(request.pod).
		SubResource("portforward").URL()

	transport, upgrader, err := spdy.RoundTripperFor(request.config)
	if err != nil {
		return fmt.Errorf("create round tripper: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", reqURL)
	ports := []string{fmt.Sprintf("%d:%d", request.localPort, request.remotePort)}

	pf, err := portforward.NewOnAddresses(dialer, []string{forwardBindAddress}, ports, stopCh, readyCh, io.Discard, io.Discard)
	if err != nil {
		return fmt.Errorf("create port forward: %w", err)
	}

	if err := pf.ForwardPorts(); err != nil {
		return fmt.Errorf("forward ports: %w", err)
	}
	return nil
}

func waitForForwardReady(ctx context.Context, readyCh <-chan struct{}, errCh <-chan error, doneCh <-chan struct{}, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-readyCh:
		return nil
	case err := <-errCh:
		return forwardExitError(err)
	case <-doneCh:
		return forwardExitError(drainError(errCh))
	case <-ctx.Done():
		return fmt.Errorf("wait for port forward ready: %w", ctx.Err())
	case <-timer.C:
		return fmt.Errorf("%w (%s)", errForwardNotReady, timeout)
	}
}

func drainError(errCh <-chan error) error {
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func forwardExitError(err error) error {
	if err == nil {
		return errForwardExitedEarly
	}
	return err
}
