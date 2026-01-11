package kubetest

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/apimachinery/pkg/util/httpstream/spdy"
	"k8s.io/client-go/tools/portforward"
)

// backendDialTimeout bounds the dial to a pod backend on loopback.
const backendDialTimeout = 5 * time.Second

// ForwardTo makes port-forward data streams of the pod connect to the TCP address, so a real server, for
// example an httptest.Server, can act as the pod: pass its Listener.Addr().String(). The remote port the
// client asks for is ignored. The setting applies to streams opened after the call.
func (s *Server) ForwardTo(namespace, pod, address string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backends[podKey(namespace, pod)] = address
}

// ForwardEcho goes back to the default behavior for the pod: every data stream echoes what it receives.
func (s *Server) ForwardEcho(namespace, pod string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.backends, podKey(namespace, pod))
}

// DropForwardConnections closes every upgraded port-forward connection, which clients see as a lost
// connection to the pod. Connections upgraded later are not affected.
func (s *Server) DropForwardConnections() {
	s.mu.Lock()
	conns := s.conns
	s.conns = nil
	s.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

// ForwardConnections returns how many upgraded port-forward connections are currently tracked.
func (s *Server) ForwardConnections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// WaitForForwardConnections blocks until at least count upgraded connections are tracked, and fails tb after
// a generous timeout. A client can see a finished upgrade slightly before the server tracks the connection,
// so wait here before DropForwardConnections. Call it from the test goroutine.
func (s *Server) WaitForForwardConnections(tb testing.TB, count int) {
	tb.Helper()
	s.waitFor(tb, &s.connsSignal, func() bool { return len(s.conns) >= count }, "port-forward connection #"+strconv.Itoa(count))
}

func (s *Server) servePortForward(w http.ResponseWriter, r *http.Request, namespace, pod string) {
	if _, found := s.GetPod(namespace, pod); !found {
		writeStatus(w, http.StatusNotFound)
		return
	}
	if _, err := httpstream.Handshake(r, w, []string{portforward.PortForwardProtocolV1Name}); err != nil {
		return
	}
	pairs := &streamPairs{errors: make(map[string]httpstream.Stream)}
	conn := spdy.NewResponseUpgrader().UpgradeResponse(w, r, func(stream httpstream.Stream, replySent <-chan struct{}) error {
		requestID := stream.Headers().Get(corev1.PortForwardRequestIDHeader)
		if stream.Headers().Get(corev1.StreamType) != corev1.StreamTypeData {
			pairs.keep(requestID, stream)
			return nil
		}
		go func() {
			<-replySent
			err := s.serveDataStream(stream, namespace, pod)
			pairs.finish(requestID, err)
		}()
		return nil
	})
	if conn == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		_ = conn.Close()
		return
	}
	s.conns = append(s.conns, conn)
	s.connsSignal.fire()
}

// streamPairs remembers the error stream of each forwarded connection, keyed by the request ID the client
// sets on both streams of a connection, as the real kubelet pairs them. The client waits on the error stream
// until the data stream is done: the kubelet writes the failure, if any, to it and closes it. Keying by port
// would mix up two overlapping connections to the same port.
type streamPairs struct {
	mu     sync.Mutex
	errors map[string]httpstream.Stream
}

func (p *streamPairs) keep(requestID string, errorStream httpstream.Stream) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.errors[requestID] = errorStream
}

func (p *streamPairs) finish(requestID string, failure error) {
	p.mu.Lock()
	errorStream := p.errors[requestID]
	delete(p.errors, requestID)
	p.mu.Unlock()
	if errorStream == nil {
		return
	}
	if failure != nil {
		_, _ = io.WriteString(errorStream, failure.Error())
	}
	_ = errorStream.Close()
}

// serveDataStream echoes the stream or copies it to and from the pod's backend until either side ends. It
// returns why the backend could not be used, if it could not.
func (s *Server) serveDataStream(stream httpstream.Stream, namespace, pod string) error {
	defer stream.Close()
	s.mu.Lock()
	address, proxied := s.backends[podKey(namespace, pod)]
	s.mu.Unlock()
	if !proxied {
		_, _ = io.Copy(stream, stream)
		return nil
	}
	backend, err := net.DialTimeout("tcp", address, backendDialTimeout)
	if err != nil {
		return err
	}
	defer backend.Close()
	go func() {
		_, _ = io.Copy(backend, stream)
		if tcp, ok := backend.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()
	_, _ = io.Copy(stream, backend)
	return nil
}

func podKey(namespace, pod string) string { return namespace + "/" + pod }
