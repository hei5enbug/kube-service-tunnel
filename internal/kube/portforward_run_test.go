package kube

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
)

type runResult struct {
	err error
}

func newForwardRequest(t *testing.T, config *rest.Config, local, remote int32) forwardRequest {
	t.Helper()
	clientset, err := kubernetes.NewForConfig(&rest.Config{Host: config.Host})
	if err != nil {
		t.Fatalf("create clientset: %v", err)
	}
	return forwardRequest{config: config, clientset: clientset, namespace: "app", pod: "web-1", localPort: local, remotePort: remote}
}

func startRunPortForward(request forwardRequest) (stopCh chan struct{}, readyCh chan struct{}, result <-chan runResult) {
	stopCh = make(chan struct{})
	readyCh = make(chan struct{})
	out := make(chan runResult, 1)
	go func() { out <- runResult{err: runPortForward(request, stopCh, readyCh)} }()
	return stopCh, readyCh, out
}

func TestRunPortForward(t *testing.T) {
	t.Run("API 서버가 업그레이드를 거부하면 포워딩 오류를 돌려주고 준비 신호는 보내지 않는다", func(t *testing.T) {
		// given
		api := newFakeAPI(t, apiFixture{failures: map[string]int{"/api/v1/namespaces/app/pods/web-1/portforward": 403}})
		request := newForwardRequest(t, &rest.Config{Host: api.URL()}, firstFreePortFrom(t, minLocalPort), 8080)
		stopCh, readyCh, result := startRunPortForward(request)
		defer close(stopCh)

		// when
		var got runResult
		select {
		case got = <-result:
		case <-time.After(testWait):
			t.Fatal("runPortForward did not return")
		}

		// then
		if got.err == nil || !strings.Contains(got.err.Error(), "forward ports") {
			t.Fatalf("err = %v, want forward ports error", got.err)
		}
		select {
		case <-readyCh:
			t.Fatal("ready signal was sent although the upgrade was rejected")
		default:
		}
		if len(api.requestsTo("/portforward")) == 0 {
			t.Fatal("port-forward endpoint was never requested")
		}
	})

	t.Run("준비되면 로컬 포트로 들어온 데이터를 파드로 전달하고 중지하면 오류 없이 끝난다", func(t *testing.T) {
		// given
		api := newFakeAPI(t, apiFixture{forward: newSPDYForwardServer(t)})
		local := firstFreePortFrom(t, minLocalPort)
		stopCh, readyCh, result := startRunPortForward(newForwardRequest(t, &rest.Config{Host: api.URL()}, local, 8080))
		select {
		case <-readyCh:
		case <-time.After(testWait):
			t.Fatal("port forward did not become ready")
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(local))), testWait)
		if err != nil {
			t.Fatalf("dial forwarded port: %v", err)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(testWait))

		// when
		_, writeErr := conn.Write([]byte("ping"))
		echoed := make([]byte, 4)
		_, readErr := conn.Read(echoed)
		close(stopCh)

		// then
		if writeErr != nil || readErr != nil || string(echoed) != "ping" {
			t.Fatalf("echo = %q, write err = %v, read err = %v, want ping", echoed, writeErr, readErr)
		}
		select {
		case got := <-result:
			if got.err != nil {
				t.Fatalf("err = %v, want nil after stop", got.err)
			}
		case <-time.After(testWait):
			t.Fatal("runPortForward did not return after stop")
		}
	})

	t.Run("준비된 뒤 파드 연결이 끊기면 연결 끊김 오류를 돌려준다", func(t *testing.T) {
		// given
		forwardServer := newSPDYForwardServer(t)
		api := newFakeAPI(t, apiFixture{forward: forwardServer})
		stopCh, readyCh, result := startRunPortForward(newForwardRequest(t, &rest.Config{Host: api.URL()}, firstFreePortFrom(t, minLocalPort), 8080))
		defer close(stopCh)
		select {
		case <-readyCh:
		case <-time.After(testWait):
			t.Fatal("port forward did not become ready")
		}

		// when
		forwardServer.dropConnections()

		// then
		select {
		case got := <-result:
			if !errors.Is(got.err, portforward.ErrLostConnectionToPod) {
				t.Fatalf("err = %v, want lost connection to pod", got.err)
			}
		case <-time.After(testWait):
			t.Fatal("runPortForward did not return after the connection dropped")
		}
	})

	t.Run("인증서 설정이 잘못되면 라운드트리퍼 생성 오류를 돌려준다", func(t *testing.T) {
		// given
		config := &rest.Config{Host: "https://127.0.0.1:1", TLSClientConfig: rest.TLSClientConfig{CAData: []byte("not a certificate")}}
		request := newForwardRequest(t, config, 40000, 8080)

		// when
		err := runPortForward(request, make(chan struct{}), make(chan struct{}))

		// then
		if err == nil || !strings.Contains(err.Error(), "create round tripper") {
			t.Fatalf("err = %v, want create round tripper error", err)
		}
	})

	t.Run("원격 포트가 0이면 포트 포워드 생성 오류를 돌려준다", func(t *testing.T) {
		// given
		request := newForwardRequest(t, &rest.Config{Host: "http://127.0.0.1:1"}, 40000, 0)

		// when
		err := runPortForward(request, make(chan struct{}), make(chan struct{}))

		// then
		if err == nil || !strings.Contains(err.Error(), "create port forward") {
			t.Fatalf("err = %v, want create port forward error", err)
		}
	})
}
