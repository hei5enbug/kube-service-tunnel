package dns

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	proxyadapter "github.com/byoungmin/kube-service-tunnel/internal/proxy"
)

const (
	// scenarioWait is an upper bound for events that depend on a cluster, such as a pod loss report.
	scenarioWait = 3 * time.Minute
	// portWait is an upper bound for a local port to open or close.
	portWait = 30 * time.Second
	// scenarioHTTPTimeout bounds one HTTP request of a scenario.
	scenarioHTTPTimeout = 5 * time.Second
	// maxPollDelay caps the growing pause between two checks of a condition.
	maxPollDelay = 100 * time.Millisecond
)

// remappedProxy runs the real proxy on a free high port instead of 80, so a scenario needs no root.
type remappedProxy struct {
	proxyadapter.ProxyAdapterInterface
	port int32
}

func (p remappedProxy) StartIfNotRunning(int32) error {
	return p.ProxyAdapterInterface.StartIfNotRunning(p.port)
}

// expectPod checks that a request reaches the named whoami pod, through the proxy when hostHeader is set.
func expectPod(t *testing.T, port int32, hostHeader, pod string) {
	t.Helper()
	status, body, err := get(port, hostHeader)
	if err != nil || status != http.StatusOK || !strings.Contains(body, "Hostname: "+pod) {
		t.Fatalf("GET 127.0.0.1:%d host %q = %d %q, err %v, want pod %s", port, hostHeader, status, firstLine(body), err, pod)
	}
}

func expectStatus(t *testing.T, port int32, hostHeader string, want int) {
	t.Helper()
	status, body, err := get(port, hostHeader)
	if err != nil || status != want {
		t.Fatalf("GET 127.0.0.1:%d host %q = %d %q, err %v, want %d", port, hostHeader, status, firstLine(body), err, want)
	}
}

// get sends one request on its own connection, so no idle connection outlives a forward.
func get(port int32, hostHeader string) (int, string, error) {
	request, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", port), nil)
	if err != nil {
		return 0, "", err
	}
	if hostHeader != "" {
		request.Host = hostHeader
	}
	client := &http.Client{
		Timeout:   scenarioHTTPTimeout,
		Transport: &http.Transport{DisableKeepAlives: true},
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return response.StatusCode, string(body), err
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

func freePort(t *testing.T) int32 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick free port: %v", err)
	}
	defer listener.Close()
	return int32(listener.Addr().(*net.TCPAddr).Port)
}

func portOpen(port int32) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// eventually checks the condition at once and then again after growing pauses. A condition that already holds
// costs no wait, and timeout is only an upper bound.
func eventually(t *testing.T, what string, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for delay := time.Millisecond; ; delay = min(delay*2, maxPollDelay) {
		if condition() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(delay)
	}
}
