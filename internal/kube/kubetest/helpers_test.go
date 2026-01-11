package kubetest

import (
	"io"
	"net/http"
	"net/url"
	"runtime"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// eventTimeout is an upper bound for waiting on something that must happen; tests do not wait for it.
const eventTimeout = 10 * time.Second

// recordingTB records Errorf and Fatalf calls instead of failing the test, so tests can check the failures
// the fixture reports. Fatalf ends the calling goroutine like testing.T does.
type recordingTB struct {
	testing.TB
	mu       sync.Mutex
	failures []string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Errorf(format string, args ...any) { r.record(format) }

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.record(format)
	runtime.Goexit()
}

func (r *recordingTB) record(message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures = append(r.failures, message)
}

func (r *recordingTB) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.failures...)
}

// runToCompletion runs fn on its own goroutine and returns after it finished or called Goexit.
func runToCompletion(fn func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	<-done
}

func newClientset(t *testing.T, server *Server) kubernetes.Interface {
	t.Helper()
	clientset, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL()})
	if err != nil {
		t.Fatalf("create clientset: %v", err)
	}
	return clientset
}

func nextEvent(t *testing.T, watcher watch.Interface) watch.Event {
	t.Helper()
	select {
	case event, open := <-watcher.ResultChan():
		if !open {
			t.Fatal("watch ended before the expected event")
		}
		return event
	case <-time.After(eventTimeout):
		t.Fatal("no watch event arrived")
		return watch.Event{}
	}
}

// forwardHandle is a client-go port forward to a pod of a Server, listening on a port the OS picked.
type forwardHandle struct {
	port int
	done <-chan error
	stop func()
}

// openForward starts a port forward and waits until it listens. It returns the ForwardPorts error when the
// forward ends before that.
func openForward(t *testing.T, server *Server, namespace, pod string) (*forwardHandle, error) {
	t.Helper()
	transport, upgrader, err := spdy.RoundTripperFor(&rest.Config{Host: server.URL()})
	if err != nil {
		t.Fatalf("create round tripper: %v", err)
	}
	target, err := url.Parse(server.URL() + "/api/v1/namespaces/" + namespace + "/pods/" + pod + "/portforward")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, target)
	stopCh, readyCh := make(chan struct{}), make(chan struct{})
	forwarder, err := portforward.NewOnAddresses(dialer, []string{"127.0.0.1"}, []string{"0:8080"}, stopCh, readyCh, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("create port forward: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- forwarder.ForwardPorts() }()

	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { close(stopCh) }) }
	t.Cleanup(stop)

	select {
	case <-readyCh:
	case err := <-done:
		return nil, err
	case <-time.After(eventTimeout):
		t.Fatal("port forward did not become ready")
	}
	ports, err := forwarder.GetPorts()
	if err != nil || len(ports) != 1 {
		t.Fatalf("GetPorts() = %v, %v", ports, err)
	}
	return &forwardHandle{port: int(ports[0].Local), done: done, stop: stop}, nil
}

// dialStreams opens a raw port-forward connection to a pod of a Server, so a test can pair the streams of
// each forwarded connection itself, as client-go does for every accepted local connection.
func dialStreams(t *testing.T, server *Server, namespace, pod string) httpstream.Connection {
	t.Helper()
	transport, upgrader, err := spdy.RoundTripperFor(&rest.Config{Host: server.URL()})
	if err != nil {
		t.Fatalf("create round tripper: %v", err)
	}
	target, err := url.Parse(server.URL() + "/api/v1/namespaces/" + namespace + "/pods/" + pod + "/portforward")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, target)
	conn, _, err := dialer.Dial(portforward.PortForwardProtocolV1Name)
	if err != nil {
		t.Fatalf("dial port forward connection: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// forwardedConnection is the error and data stream of one connection through a forward.
type forwardedConnection struct {
	errors httpstream.Stream
	data   httpstream.Stream
}

// openConnection creates the two streams of a connection to the remote port, tied together by requestID.
func openConnection(t *testing.T, conn httpstream.Connection, requestID, remotePort string) forwardedConnection {
	t.Helper()
	headers := http.Header{}
	headers.Set(corev1.StreamType, corev1.StreamTypeError)
	headers.Set(corev1.PortHeader, remotePort)
	headers.Set(corev1.PortForwardRequestIDHeader, requestID)
	errorStream, err := conn.CreateStream(headers)
	if err != nil {
		t.Fatalf("create error stream: %v", err)
	}
	_ = errorStream.Close()
	headers.Set(corev1.StreamType, corev1.StreamTypeData)
	dataStream, err := conn.CreateStream(headers)
	if err != nil {
		t.Fatalf("create data stream: %v", err)
	}
	return forwardedConnection{errors: errorStream, data: dataStream}
}

// echoed writes message to the data stream and reads the same number of bytes back.
func (c forwardedConnection) echoed(t *testing.T, message string) string {
	t.Helper()
	if _, err := io.WriteString(c.data, message); err != nil {
		t.Fatalf("write to data stream: %v", err)
	}
	reply := make([]byte, len(message))
	if _, err := io.ReadFull(c.data, reply); err != nil {
		t.Fatalf("read from data stream: %v", err)
	}
	return string(reply)
}

// finish ends the client side of the data stream, as client-go does when the local connection closes, and
// returns what the server wrote to the error stream before closing it.
func (c forwardedConnection) finish(t *testing.T) string {
	t.Helper()
	_ = c.data.Close()
	result := make(chan string, 1)
	go func() {
		message, _ := io.ReadAll(c.errors)
		result <- string(message)
	}()
	select {
	case message := <-result:
		return message
	case <-time.After(eventTimeout):
		t.Fatal("error stream was not closed after its data stream ended")
		return ""
	}
}
