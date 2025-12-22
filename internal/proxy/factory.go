package proxy

func NewProxyServer() *ProxyServer {
	return &ProxyServer{
		routes: make(map[string]int32),
	}
}

