package instance

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
)

const (
	DefaultLockPath = "/var/run/kube-service-tunnel.lock"
	lockFileMode    = 0o644
)

var ErrAlreadyRunning = errors.New("another kube-service-tunnel instance is already running")

type Lock struct {
	file      *os.File
	once      sync.Once
	releaseEr error
}

// Acquire takes an exclusive, non-blocking lock on path that lasts until Release or process exit.
func Acquire(path string) (*Lock, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, lockFileMode)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		closeErr := file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w (lock file %s)", ErrAlreadyRunning, path)
		}
		return nil, errors.Join(fmt.Errorf("lock %s: %w", path, err), closeErr)
	}

	return &Lock{file: file}, nil
}

// Release drops the lock. Calling it more than once returns the first result.
func (l *Lock) Release() error {
	l.once.Do(func() {
		if err := l.file.Close(); err != nil {
			l.releaseEr = fmt.Errorf("release lock: %w", err)
		}
	})
	return l.releaseEr
}
