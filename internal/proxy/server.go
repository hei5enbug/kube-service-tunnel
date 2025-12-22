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

type ProxyServer struct {
	server   *http.Server
	routes   map[string]int32
	mu       sync.RWMutex
	port     int32
	listener net.Listener
}

func (p *ProxyServer) Start(port int32) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.listener != nil {
		return fmt.Errorf("proxy server already running")
	}

	p.port = port
	p.routes = make(map[string]int32)

	mux := http.NewServeMux()
	mux.HandleFunc("/", p.handleRequest)

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

func (p *ProxyServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	hostWithPort := r.Host
	host := hostWithPort
	if strings.Contains(hostWithPort, ":") {
		parts := strings.Split(hostWithPort, ":")
		host = parts[0]
	}

	p.mu.RLock()
	localPort, exists := p.routes[hostWithPort]
	if !exists {
		localPort, exists = p.routes[host]
	}
	p.mu.RUnlock()

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

func (p *ProxyServer) Stop() error {
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

func (p *ProxyServer) UpdateRoutes(routes map[string]int32) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.routes = make(map[string]int32)
	for k, v := range routes {
		p.routes[k] = v
	}
}

func (p *ProxyServer) GetPort() int32 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.port
}

func (p *ProxyServer) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.listener != nil
}

func (p *ProxyServer) GetRoutes() map[string]int32 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make(map[string]int32)
	for k, v := range p.routes {
		result[k] = v
	}
	return result
}

func (p *ProxyServer) AddRoute(host string, localPort int32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.routes[host] = localPort
}

func (p *ProxyServer) RemoveRoute(host string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.routes, host)
}

