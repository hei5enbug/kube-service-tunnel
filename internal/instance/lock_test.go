package instance

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestAcquire(t *testing.T) {
	t.Run("같은 경로를 두 번째로 획득하면 ErrAlreadyRunning을 반환한다", func(t *testing.T) {
		// given
		path := filepath.Join(t.TempDir(), "app.lock")
		first, err := Acquire(path)
		if err != nil {
			t.Fatalf("first acquire: %v", err)
		}
		defer first.Release()

		// when
		second, err := Acquire(path)

		// then
		if !errors.Is(err, ErrAlreadyRunning) {
			t.Fatalf("err = %v, want ErrAlreadyRunning", err)
		}
		if second != nil {
			t.Fatal("expected nil lock on failure")
		}
	})

	t.Run("Release 후에는 다시 획득할 수 있다", func(t *testing.T) {
		// given
		path := filepath.Join(t.TempDir(), "app.lock")
		first, err := Acquire(path)
		if err != nil {
			t.Fatalf("first acquire: %v", err)
		}
		if err := first.Release(); err != nil {
			t.Fatalf("release: %v", err)
		}

		// when
		second, err := Acquire(path)

		// then
		if err != nil {
			t.Fatalf("second acquire: %v", err)
		}
		defer second.Release()
	})

	t.Run("Release를 두 번 호출해도 같은 결과를 반환한다", func(t *testing.T) {
		// given
		path := filepath.Join(t.TempDir(), "app.lock")
		lock, err := Acquire(path)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		firstErr := lock.Release()

		// when
		secondErr := lock.Release()

		// then
		if firstErr != nil || secondErr != nil {
			t.Fatalf("release errors: first=%v second=%v", firstErr, secondErr)
		}
	})

	t.Run("락 파일 경로가 심볼릭 링크면 따라가지 않고 실패한다", func(t *testing.T) {
		// given
		dir := t.TempDir()
		target := filepath.Join(dir, "target")
		link := filepath.Join(dir, "app.lock")
		if err := os.WriteFile(target, nil, 0o644); err != nil {
			t.Fatalf("write target: %v", err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("symlink: %v", err)
		}

		// when
		lock, err := Acquire(link)

		// then
		if err == nil {
			_ = lock.Release()
			t.Fatal("expected error for symlinked lock path")
		}
		if errors.Is(err, ErrAlreadyRunning) {
			t.Fatalf("unexpected ErrAlreadyRunning: %v", err)
		}
	})

	t.Run("디렉터리가 없으면 오류를 반환한다", func(t *testing.T) {
		// given
		path := filepath.Join(t.TempDir(), "missing", "app.lock")

		// when
		lock, err := Acquire(path)

		// then
		if err == nil {
			_ = lock.Release()
			t.Fatal("expected error for missing directory")
		}
	})
}

func TestAcquireLockFailure(t *testing.T) {
	t.Run("락 시스템 호출이 EWOULDBLOCK이 아닌 오류로 실패하면 ErrAlreadyRunning 없이 감싼 오류를 반환한다", func(t *testing.T) {
		// given
		cause := syscall.EIO
		original := flock
		flock = func(int, int) error { return cause }
		t.Cleanup(func() { flock = original })
		path := filepath.Join(t.TempDir(), "app.lock")

		// when
		lock, err := Acquire(path)

		// then
		if lock != nil {
			t.Fatal("expected nil lock on failure")
		}
		if errors.Is(err, ErrAlreadyRunning) {
			t.Fatalf("unexpected ErrAlreadyRunning: %v", err)
		}
		if !errors.Is(err, cause) || !strings.Contains(err.Error(), "lock "+path) {
			t.Fatalf("err = %v, want wrapped %v", err, cause)
		}
	})

	t.Run("락 파일 경로가 디렉터리면 열기 오류를 경로와 함께 반환한다", func(t *testing.T) {
		// given
		path := t.TempDir()

		// when
		lock, err := Acquire(path)

		// then
		if lock != nil {
			t.Fatal("expected nil lock on failure")
		}
		if err == nil || !strings.Contains(err.Error(), "open lock file "+path) {
			t.Fatalf("err = %v, want open lock file error", err)
		}
	})
}

func TestRelease(t *testing.T) {
	t.Run("이미 닫힌 파일이면 감싼 오류를 반환하고 다시 호출해도 같은 오류를 반환한다", func(t *testing.T) {
		// given
		path := filepath.Join(t.TempDir(), "app.lock")
		lock, err := Acquire(path)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		if err := lock.file.Close(); err != nil {
			t.Fatalf("close file: %v", err)
		}

		// when
		first := lock.Release()
		second := lock.Release()

		// then
		if first == nil || !strings.Contains(first.Error(), "release lock") {
			t.Fatalf("first = %v, want release lock error", first)
		}
		if second != first {
			t.Fatalf("second = %v, want the first result %v", second, first)
		}
	})

	t.Run("파일이 없는 락은 패닉 없이 오류를 반환한다", func(t *testing.T) {
		// given
		lock := &Lock{}

		// when
		err := lock.Release()

		// then
		if err == nil {
			t.Fatal("expected error for lock without file")
		}
	})
}
