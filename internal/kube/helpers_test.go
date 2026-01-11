package kube

import (
	"net"
	"strconv"
	"testing"
	"time"
)

// testWait is the upper bound for anything a test waits for; passing tests never wait for it.
const testWait = 10 * time.Second

// quietPeriod is how long a test watches for something that must not happen. It only bounds how much a
// missed regression can slip through; it never decides whether a passing scenario succeeds.
const quietPeriod = 25 * time.Millisecond

// fastRetry is the retry and stop delay tests inject in place of the production seconds.
const fastRetry = 5 * time.Millisecond

// freeLocalPort returns a port in the automatic range that the OS just reported as free on 127.0.0.1.
// The OS picks the port, so parallel tests get different ports in practice.
func freeLocalPort(t testing.TB) int32 {
	t.Helper()
	for attempt := 0; attempt < 200; attempt++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		port := int32(listener.Addr().(*net.TCPAddr).Port)
		_ = listener.Close()
		if port >= minLocalPort && port < maxLocalPort {
			return port
		}
	}
	t.Fatalf("the OS assigned no port in %d-%d", minLocalPort, maxLocalPort)
	return 0
}

// occupyLocalPort listens on a free port of the automatic range until the test ends and returns it.
func occupyLocalPort(t testing.TB) int32 {
	t.Helper()
	for attempt := 0; attempt < 20; attempt++ {
		port := freeLocalPort(t)
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
		if err != nil {
			continue
		}
		t.Cleanup(func() { _ = listener.Close() })
		return port
	}
	t.Fatal("could not occupy a port of the automatic range")
	return 0
}

// eventually polls condition until it holds. Polling is only the way to observe state that has no signal.
func eventually(t testing.TB, condition func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(testWait)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// receive returns the next value of ch, failing the test when none arrives within testWait.
func receive[T any](t testing.TB, ch <-chan T, what string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(testWait):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

// closed fails the test unless ch is closed (or receives) within testWait.
func closed[T any](t testing.TB, ch <-chan T, what string) {
	t.Helper()
	receive(t, ch, what)
}

// expectNone fails the test when ch already holds a value. Call it only after a completion point, such as
// awaitBackground, proved that nothing can still send.
func expectNone[T any](t testing.TB, ch <-chan T, what string) {
	t.Helper()
	select {
	case value := <-ch:
		t.Fatalf("unexpected %s: %+v", what, value)
	default:
	}
}

// expectNoneWithin fails the test when ch delivers a value during quietPeriod. Use it only where the
// observed goroutine keeps running by design, so no completion point exists.
func expectNoneWithin[T any](t testing.TB, ch <-chan T, what string) {
	t.Helper()
	select {
	case value := <-ch:
		t.Fatalf("unexpected %s: %+v", what, value)
	case <-time.After(quietPeriod):
	}
}

// awaitBackground blocks until every forward watcher and pod monitor of the client has returned, lost handler
// calls included. The forwards must have been stopped or lost, or the watchers never return.
func awaitBackground(t testing.TB, client *portForwardClient) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		client.background.Wait()
		close(done)
	}()
	closed(t, done, "forward watchers and pod monitors to finish")
}
