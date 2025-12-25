package proxy

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
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
	err := p.start(port)
	if err != nil && err.Error() == "proxy server already running" {
		return nil
	}
	return err
}

func (p *proxyAdapter) start(port int32) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.listener != nil {
		return fmt.Errorf("proxy server already running")
	}

	p.port = port
	p.routes = make(map[string]int32)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		HandleProxyRequest(p.routes, &p.mu, w, r)
	})

	p.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	listener, err := net.Listen("tcp", p.server.Addr)
	if err != nil {
		return fmt.Errorf("listen on port %d: %w", port, err)
	}

	p.listener = listener

	go func() {
		if err := p.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Printf("proxy server error: %v\n", err)
		}
	}()

	return nil
}

func (p *proxyAdapter) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.server == nil {
		return nil
	}

	if p.listener != nil {
		if err := p.listener.Close(); err != nil {
			return fmt.Errorf("close listener: %w", err)
		}
	}

	if err := p.server.Close(); err != nil {
		return fmt.Errorf("close server: %w", err)
	}

	p.listener = nil
	p.server = nil
	p.routes = make(map[string]int32)

	return nil
}

func (p *proxyAdapter) AddRoute(host string, localPort int32) {
	p.AddRoutes(map[string]int32{host: localPort})
}

func (p *proxyAdapter) AddRoutes(routes map[string]int32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for host, port := range routes {
		p.routes[host] = port
	}
}

func (p *proxyAdapter) RemoveRoute(host string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.routes, host)
}

func HandleProxyRequest(routes map[string]int32, mu *sync.RWMutex, w http.ResponseWriter, r *http.Request) {
	hostWithPort := r.Host
	host := hostWithPort
	if strings.Contains(hostWithPort, ":") {
		parts := strings.Split(hostWithPort, ":")
		host = parts[0]
	}

	mu.RLock()
	localPort, exists := routes[hostWithPort]
	if !exists {
		localPort, exists = routes[host]
	}
	mu.RUnlock()

	if !exists {
		http.Error(w, fmt.Sprintf("no route found for host: %s", hostWithPort), http.StatusNotFound)
		return
	}

	targetURL, err := url.Parse(fmt.Sprintf("http://localhost:%d", localPort))
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid target URL: %v", err), http.StatusInternalServerError)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.ServeHTTP(w, r)
}
