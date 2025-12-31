package kube

import (
	"fmt"
	"net"
	"testing"
)

func firstFreePortFrom(t *testing.T, start int32) int32 {
	t.Helper()
	for port := start; port <= maxLocalPort; port++ {
		if isLocalPortFree(port) {
			return port
		}
	}
	t.Fatalf("no free port from %d", start)
	return 0
}

func TestFindAvailablePort(t *testing.T) {
	t.Run("사용 중으로 표시된 포트는 건너뛴다", func(t *testing.T) {
		// given
		free := firstFreePortFrom(t, minLocalPort)
		used := map[int32]bool{free: true}

		// when
		got, err := findAvailablePort(free, used)

		// then
		if err != nil || got == free {
			t.Fatalf("port = %d, err = %v, want a port other than %d", got, err, free)
		}
	})

	t.Run("로컬 주소에서 이미 점유된 포트는 건너뛴다", func(t *testing.T) {
		// given
		free := firstFreePortFrom(t, minLocalPort)
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", free))
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		defer listener.Close()

		// when
		got, err := findAvailablePort(free, nil)

		// then
		if err != nil || got == free {
			t.Fatalf("port = %d, err = %v, want a port other than %d", got, err, free)
		}
	})

	t.Run("시작 포트가 40000 미만이면 40000부터 찾는다", func(t *testing.T) {
		// given
		start := int32(1024)

		// when
		got, err := findAvailablePort(start, nil)

		// then
		if err != nil || got < minLocalPort {
			t.Fatalf("port = %d, err = %v, want >= %d", got, err, minLocalPort)
		}
	})

	t.Run("65535 포트도 후보에 포함한다", func(t *testing.T) {
		// given
		if !isLocalPortFree(maxLocalPort) {
			t.Skip("port 65535 is occupied on this machine")
		}

		// when
		got, err := findAvailablePort(maxLocalPort, nil)

		// then
		if err != nil || got != maxLocalPort {
			t.Fatalf("port = %d, err = %v, want %d", got, err, maxLocalPort)
		}
	})

	t.Run("후보가 모두 사용 중이면 오류를 낸다", func(t *testing.T) {
		// given
		used := map[int32]bool{maxLocalPort: true}

		// when
		_, err := findAvailablePort(maxLocalPort, used)

		// then
		if err == nil {
			t.Fatal("err = nil, want error")
		}
	})
}
