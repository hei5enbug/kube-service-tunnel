package kube

import "testing"

func TestFindAvailablePort(t *testing.T) {
	t.Parallel()

	t.Run("사용 중으로 표시된 포트는 건너뛴다", func(t *testing.T) {
		t.Parallel()
		// given
		free := freeLocalPort(t)
		used := map[int32]bool{free: true}

		// when
		got, err := findAvailablePort(free, used)

		// then
		if err != nil || got == free {
			t.Fatalf("port = %d, err = %v, want a port other than %d", got, err, free)
		}
	})

	t.Run("로컬 주소에서 이미 점유된 포트는 건너뛴다", func(t *testing.T) {
		t.Parallel()
		// given
		free := occupyLocalPort(t)

		// when
		got, err := findAvailablePort(free, nil)

		// then
		if err != nil || got == free {
			t.Fatalf("port = %d, err = %v, want a port other than %d", got, err, free)
		}
	})

	t.Run("시작 포트가 40000 미만이면 40000부터 찾는다", func(t *testing.T) {
		t.Parallel()
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
		t.Parallel()
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
		t.Parallel()
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
