package instance

import (
	"errors"
	"os"
	"path/filepath"
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
