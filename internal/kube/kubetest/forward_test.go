package kubetest

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/portforward"
)

func podServer(t *testing.T) *Server {
	t.Helper()
	return NewServer(t, Config{Pods: map[string][]corev1.Pod{"app": {RunningPod("app", "web-1", nil, true)}}})
}

func dialLocal(t *testing.T, port int) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), eventTimeout)
	if err != nil {
		t.Fatalf("dial forwarded port: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(eventTimeout))
	return conn
}

func exchange(conn net.Conn, message string) (string, error) {
	if _, err := conn.Write([]byte(message)); err != nil {
		return "", err
	}
	reply := make([]byte, len(message))
	_, err := io.ReadFull(conn, reply)
	return string(reply), err
}

func TestServer_PortForward(t *testing.T) {
	t.Parallel()

	t.Run("기본값은 데이터 스트림으로 받은 바이트를 그대로 되돌려 준다", func(t *testing.T) {
		t.Parallel()
		// given
		server := podServer(t)
		forward, err := openForward(t, server, "app", "web-1")
		if err != nil {
			t.Fatalf("openForward() error = %v", err)
		}
		conn := dialLocal(t, forward.port)

		// when
		reply, exchangeErr := exchange(conn, "ping")

		// then
		if exchangeErr != nil || reply != "ping" {
			t.Fatalf("reply = %q, %v, want ping", reply, exchangeErr)
		}
	})

	t.Run("ForwardTo로 지정한 TCP 서버가 파드처럼 HTTP 요청에 답한다", func(t *testing.T) {
		t.Parallel()
		// given
		pod := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "hello from pod "+r.URL.Path)
		}))
		t.Cleanup(pod.Close)
		server := podServer(t)
		server.ForwardTo("app", "web-1", pod.Listener.Addr().String())
		forward, err := openForward(t, server, "app", "web-1")
		if err != nil {
			t.Fatalf("openForward() error = %v", err)
		}
		client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: eventTimeout}

		// when
		response, getErr := client.Get("http://127.0.0.1:" + strconv.Itoa(forward.port) + "/status")

		// then
		if getErr != nil {
			t.Fatalf("GET through forward: %v", getErr)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		if response.StatusCode != http.StatusOK || string(body) != "hello from pod /status" {
			t.Fatalf("status = %d, body = %q, want the pod's answer", response.StatusCode, body)
		}
	})

	t.Run("ForwardEcho로 되돌리면 이후 연결은 다시 에코한다", func(t *testing.T) {
		t.Parallel()
		// given
		server := podServer(t)
		server.ForwardTo("app", "web-1", "127.0.0.1:1")
		server.ForwardEcho("app", "web-1")
		forward, err := openForward(t, server, "app", "web-1")
		if err != nil {
			t.Fatalf("openForward() error = %v", err)
		}
		conn := dialLocal(t, forward.port)

		// when
		reply, exchangeErr := exchange(conn, "ping")

		// then
		if exchangeErr != nil || reply != "ping" {
			t.Fatalf("reply = %q, %v, want ping", reply, exchangeErr)
		}
	})

	t.Run("파드를 삭제해도 이미 열린 포워딩 연결은 계속 쓸 수 있다", func(t *testing.T) {
		t.Parallel()
		// given
		server := podServer(t)
		forward, err := openForward(t, server, "app", "web-1")
		if err != nil {
			t.Fatalf("openForward() error = %v", err)
		}
		conn := dialLocal(t, forward.port)
		if reply, exchangeErr := exchange(conn, "one"); exchangeErr != nil || reply != "one" {
			t.Fatalf("first exchange = %q, %v", reply, exchangeErr)
		}

		// when
		server.DeletePod("app", "web-1")
		reply, exchangeErr := exchange(conn, "two")

		// then
		if exchangeErr != nil || reply != "two" {
			t.Fatalf("exchange after delete = %q, %v, want two", reply, exchangeErr)
		}
		select {
		case err := <-forward.done:
			t.Fatalf("forward ended after the pod was deleted: %v", err)
		default:
		}
		if server.ForwardConnections() != 1 {
			t.Fatalf("ForwardConnections() = %d, want 1", server.ForwardConnections())
		}
	})

	t.Run("삭제된 파드로 새 포워딩을 열면 거절한다", func(t *testing.T) {
		t.Parallel()
		// given
		server := podServer(t)
		server.DeletePod("app", "web-1")

		// when
		forward, err := openForward(t, server, "app", "web-1")

		// then
		if err == nil || forward != nil {
			t.Fatalf("openForward() = %v, %v, want an error for the deleted pod", forward, err)
		}
	})

	t.Run("DropForwardConnections를 호출하면 클라이언트가 파드 연결 끊김 오류를 받는다", func(t *testing.T) {
		t.Parallel()
		// given
		server := podServer(t)
		forward, err := openForward(t, server, "app", "web-1")
		if err != nil {
			t.Fatalf("openForward() error = %v", err)
		}
		server.WaitForForwardConnections(t, 1)

		// when
		server.DropForwardConnections()

		// then
		select {
		case got := <-forward.done:
			if !errors.Is(got, portforward.ErrLostConnectionToPod) {
				t.Fatalf("err = %v, want lost connection to pod", got)
			}
		case <-time.After(eventTimeout):
			t.Fatal("forward did not end after the connections were dropped")
		}
		if server.ForwardConnections() != 0 {
			t.Fatalf("ForwardConnections() = %d, want 0", server.ForwardConnections())
		}
	})

	t.Run("연결할 수 없는 백엔드면 데이터 스트림을 닫아 클라이언트가 응답을 받지 못한다", func(t *testing.T) {
		t.Parallel()
		// given
		unused, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		address := unused.Addr().String()
		_ = unused.Close()
		server := podServer(t)
		server.ForwardTo("app", "web-1", address)
		forward, openErr := openForward(t, server, "app", "web-1")
		if openErr != nil {
			t.Fatalf("openForward() error = %v", openErr)
		}
		conn := dialLocal(t, forward.port)

		// when
		reply, exchangeErr := exchange(conn, "ping")

		// then
		if exchangeErr == nil || reply == "ping" {
			t.Fatalf("reply = %q, err = %v, want the connection to end without an answer", reply, exchangeErr)
		}
	})

	t.Run("프로토콜 헤더가 없는 요청은 업그레이드하지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		server := podServer(t)

		// when
		response, err := http.Post(server.URL()+"/api/v1/namespaces/app/pods/web-1/portforward", "", nil)

		// then
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer response.Body.Close()
		if response.StatusCode == http.StatusSwitchingProtocols || server.ForwardConnections() != 0 {
			t.Fatalf("status = %d, connections = %d, want a refusal", response.StatusCode, server.ForwardConnections())
		}
	})

	t.Run("업그레이드 헤더가 없으면 연결을 추적하지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		server := podServer(t)
		request, _ := http.NewRequest(http.MethodPost, server.URL()+"/api/v1/namespaces/app/pods/web-1/portforward", nil)
		request.Header.Set("X-Stream-Protocol-Version", portforward.PortForwardProtocolV1Name)

		// when
		response, err := http.DefaultClient.Do(request)

		// then
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer response.Body.Close()
		if response.StatusCode == http.StatusSwitchingProtocols || server.ForwardConnections() != 0 {
			t.Fatalf("status = %d, connections = %d, want a refusal", response.StatusCode, server.ForwardConnections())
		}
	})

	t.Run("서버가 닫히는 중에 업그레이드된 연결은 추적하지 않고 곧바로 닫는다", func(t *testing.T) {
		t.Parallel()
		// given
		server := podServer(t)
		server.mu.Lock()
		server.closed = true
		server.mu.Unlock()

		// when
		forward, err := openForward(t, server, "app", "web-1")

		// then
		if err == nil {
			select {
			case err := <-forward.done:
				t.Logf("forward on a closing server ended with %v", err)
			case <-time.After(eventTimeout):
				t.Fatal("forward stayed open on a closing server")
			}
		}
		if server.ForwardConnections() != 0 {
			t.Fatalf("ForwardConnections() = %d, want 0", server.ForwardConnections())
		}
	})

	t.Run("WaitForForwardConnections는 연결이 없으면 제한 시간 뒤 테스트를 실패로 보고한다", func(t *testing.T) {
		t.Parallel()
		// given
		recorder := &recordingTB{TB: t}
		server := NewServer(recorder, Config{})
		server.waitTimeout = 20 * time.Millisecond

		// when
		runToCompletion(func() { server.WaitForForwardConnections(recorder, 1) })

		// then
		if len(recorder.recorded()) != 1 {
			t.Fatalf("failures = %v, want exactly one", recorder.recorded())
		}
	})
}

func TestServer_PortForwardStreamPairing(t *testing.T) {
	t.Parallel()
	const remotePort = "8080"

	t.Run("같은 포트로 겹쳐 열린 두 연결은 각자의 데이터 스트림이 끝날 때 각자의 에러 스트림이 닫힌다", func(t *testing.T) {
		t.Parallel()
		// given
		server := podServer(t)
		conn := dialStreams(t, server, "app", "web-1")
		first := openConnection(t, conn, "1", remotePort)
		second := openConnection(t, conn, "2", remotePort)
		if first.echoed(t, "one") != "one" || second.echoed(t, "two") != "two" {
			t.Fatal("both connections should echo while they overlap")
		}

		// when
		firstResult := first.finish(t)
		secondStillEchoes := second.echoed(t, "again")
		secondResult := second.finish(t)

		// then
		if firstResult != "" || secondResult != "" || secondStillEchoes != "again" {
			t.Fatalf("results = %q and %q, echo = %q, want no errors and a working second connection", firstResult, secondResult, secondStillEchoes)
		}
	})

	t.Run("한 연결의 백엔드가 실패해도 같은 포트의 다른 연결은 영향받지 않고 각자 자기 오류를 받는다", func(t *testing.T) {
		t.Parallel()
		// given
		server := podServer(t)
		conn := dialStreams(t, server, "app", "web-1")
		healthy := openConnection(t, conn, "1", remotePort)
		if healthy.echoed(t, "one") != "one" {
			t.Fatal("the healthy connection should echo")
		}
		unused, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		address := unused.Addr().String()
		_ = unused.Close()
		server.ForwardTo("app", "web-1", address)

		// when
		failing := openConnection(t, conn, "2", remotePort)
		failure := failing.finish(t)
		healthyStillEchoes := healthy.echoed(t, "again")
		healthyResult := healthy.finish(t)

		// then
		if failure == "" {
			t.Fatal("the connection with the unreachable backend got no error")
		}
		if healthyResult != "" || healthyStillEchoes != "again" {
			t.Fatalf("healthy result = %q, echo = %q, want no error and a working connection", healthyResult, healthyStillEchoes)
		}
	})
}
