package proxy

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	listenHost        = "127.0.0.1"
	readHeaderTimeout = 10 * time.Second
)

type ProxyAdapterInterface interface {
	StartIfNotRunning(port int32) error
	Stop() error
	AddRoute(host string, localPort int32)
	AddRoutes(routes map[string]int32)
	RemoveRoute(host string)
}

type proxyAdapter struct {
	server   *http.Server
	listener net.Listener
	done     chan struct{}
	routes   map[string]int32
	mu       sync.RWMutex
	port     int32
}

func NewProxyAdapter() ProxyAdapterInterface {
	return &proxyAdapter{
		routes: make(map[string]int32),
	}
}

func (p *proxyAdapter) StartIfNotRunning(port int32) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.server != nil {
		return nil
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", listenHost, port))
	if err != nil {
		return fmt.Errorf("listen on port %d: %w", port, err)
	}

	server := &http.Server{
		Handler:           http.HandlerFunc(p.handle),
		ReadHeaderTimeout: readHeaderTimeout,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	done := make(chan struct{})

	p.port = port
	p.server = server
	p.listener = listener
	p.done = done

	go p.serve(server, listener, done)

	return nil
}

func (p *proxyAdapter) serve(server *http.Server, listener net.Listener, done chan struct{}) {
	err := server.Serve(listener)
	close(done)
	if !errors.Is(err, http.ErrServerClosed) {
		p.markStopped(server)
	}
}

// markStopped clears the running state only when the adapter still points at the failed server.
func (p *proxyAdapter) markStopped(server *http.Server) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.server != server {
		return
	}
	p.server = nil
	p.listener = nil
	p.done = nil
}

func (p *proxyAdapter) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.server == nil {
		return nil
	}

	server, done := p.server, p.done
	p.server = nil
	p.listener = nil
	p.done = nil
	p.routes = make(map[string]int32)

	err := server.Close()
	<-done
	if err != nil {
		return fmt.Errorf("close server: %w", err)
	}
	return nil
}

func (p *proxyAdapter) addr() string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.listener == nil {
		return ""
	}
	return p.listener.Addr().String()
}

func (p *proxyAdapter) AddRoute(host string, localPort int32) {
	p.AddRoutes(map[string]int32{host: localPort})
}

func (p *proxyAdapter) AddRoutes(routes map[string]int32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for host, port := range routes {
		p.routes[normalizeHost(host)] = port
	}
}

func (p *proxyAdapter) RemoveRoute(host string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.routes, normalizeHost(host))
}

// normalizeHost drops the port when present, lowercases and strips one trailing dot.
func normalizeHost(hostport string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

func (p *proxyAdapter) lookup(host string) (int32, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	port, ok := p.routes[host]
	return port, ok
}

func (p *proxyAdapter) handle(w http.ResponseWriter, r *http.Request) {
	localPort, exists := p.lookup(normalizeHost(r.Host))
	if !exists {
		http.Error(w, fmt.Sprintf("no route found for host: %s", r.Host), http.StatusNotFound)
		return
	}

	targetURL := &url.URL{Scheme: "http", Host: net.JoinHostPort(listenHost, fmt.Sprint(localPort))}
	reverse := httputil.NewSingleHostReverseProxy(targetURL)
	reverse.ErrorLog = log.New(io.Discard, "", 0)
	reverse.ServeHTTP(w, r)
}
