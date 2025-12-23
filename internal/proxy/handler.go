package proxy

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
)

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

