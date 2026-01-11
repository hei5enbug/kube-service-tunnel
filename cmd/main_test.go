package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/byoungmin/kube-service-tunnel/internal/instance"
	"k8s.io/klog/v2"
)

type runFixture struct {
	deps      runDeps
	hostsPath string
	lockPath  string
	tuiCalls  []string
	// events records the order of silenceKubeLogs and runTUI calls.
	events []string
}

func newRunFixture(t *testing.T) *runFixture {
	t.Helper()
	dir := t.TempDir()
	f := &runFixture{
		hostsPath: filepath.Join(dir, "hosts"),
		lockPath:  filepath.Join(dir, "app.lock"),
	}
	if err := os.WriteFile(f.hostsPath, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatalf("write hosts: %v", err)
	}
	f.deps = runDeps{
		name:      "kube-service-tunnel",
		hostsPath: f.hostsPath,
		lockPath:  f.lockPath,
		acquire:   acquireInstanceLock,
		runTUI: func(kubeconfigPath string) error {
			f.tuiCalls = append(f.tuiCalls, kubeconfigPath)
			f.events = append(f.events, "runTUI")
			return nil
		},
		silenceKubeLogs: func() { f.events = append(f.events, "silenceKubeLogs") },
	}
	return f
}

func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("hosts 파일에 쓸 수 없으면 기존 안내 메시지와 함께 1을 반환하고 TUI를 실행하지 않는다", func(t *testing.T) {
		t.Parallel()

		// given
		f := newRunFixture(t)
		f.deps.hostsPath = filepath.Join(t.TempDir(), "missing-hosts")
		var stderr bytes.Buffer

		// when
		code := run(nil, &stderr, f.deps)

		// then
		out := stderr.String()
		if code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		for _, want := range []string{"Error: cannot write to " + f.deps.hostsPath, "Please run this program with sudo or ensure you have write permission to /etc/hosts"} {
			if !strings.Contains(out, want) {
				t.Fatalf("stderr = %q, want to contain %q", out, want)
			}
		}
		if len(f.tuiCalls) != 0 {
			t.Fatalf("TUI must not run, calls = %v", f.tuiCalls)
		}
	})

	t.Run("다른 인스턴스가 실행 중이면 기존 메시지와 함께 1을 반환한다", func(t *testing.T) {
		t.Parallel()

		// given
		f := newRunFixture(t)
		held, err := instance.Acquire(f.lockPath)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		t.Cleanup(func() { releaseOrFail(t, held) })
		var stderr bytes.Buffer

		// when
		code := run(nil, &stderr, f.deps)

		// then
		if code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		if want := "Error: another kube-service-tunnel instance is already running\n"; stderr.String() != want {
			t.Fatalf("stderr = %q, want %q", stderr.String(), want)
		}
		if len(f.tuiCalls) != 0 {
			t.Fatalf("TUI must not run, calls = %v", f.tuiCalls)
		}
	})

	t.Run("락 획득이 그 밖의 이유로 실패하면 오류를 출력하고 1을 반환한다", func(t *testing.T) {
		t.Parallel()

		// given
		f := newRunFixture(t)
		f.deps.acquire = func(string) (releaser, error) { return nil, errors.New("lock exploded") }
		var stderr bytes.Buffer

		// when
		code := run(nil, &stderr, f.deps)

		// then
		if code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		if want := "Error: lock exploded\n"; stderr.String() != want {
			t.Fatalf("stderr = %q, want %q", stderr.String(), want)
		}
		if len(f.tuiCalls) != 0 {
			t.Fatalf("TUI must not run, calls = %v", f.tuiCalls)
		}
	})

	t.Run("종료할 때 락 해제에 실패하면 경고를 출력하고 종료 코드는 그대로 둔다", func(t *testing.T) {
		t.Parallel()

		// given
		f := newRunFixture(t)
		f.deps.acquire = func(string) (releaser, error) { return failingRelease{}, nil }
		var stderr bytes.Buffer

		// when
		code := run(nil, &stderr, f.deps)

		// then
		if code != 0 {
			t.Fatalf("code = %d, want 0", code)
		}
		if want := "Warning: release lock: close failed\n"; stderr.String() != want {
			t.Fatalf("stderr = %q, want %q", stderr.String(), want)
		}
		if len(f.tuiCalls) != 1 {
			t.Fatalf("TUI calls = %v, want one", f.tuiCalls)
		}
	})

	t.Run("TUI가 오류를 반환하면 Error 접두사와 함께 출력하고 락을 해제한 뒤 1을 반환한다", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			tuiErr  error
			wantOut string
		}{
			{"단일 오류", errors.New("boom"), "Error: boom\n"},
			{"합쳐진 오류", errors.Join(errors.New("run failed"), errors.New("cleanup failed")), "Error: run failed\ncleanup failed\n"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				// given
				f := newRunFixture(t)
				f.deps.runTUI = func(string) error { return tt.tuiErr }
				var stderr bytes.Buffer

				// when
				code := run(nil, &stderr, f.deps)

				// then
				if code != 1 {
					t.Fatalf("code = %d, want 1", code)
				}
				if stderr.String() != tt.wantOut {
					t.Fatalf("stderr = %q, want %q", stderr.String(), tt.wantOut)
				}
				assertLockReleased(t, f.lockPath)
			})
		}
	})

	t.Run("성공하면 0을 반환하고 TUI가 실행되는 동안 락을 잡고 이후에 해제한다", func(t *testing.T) {
		t.Parallel()

		// given
		f := newRunFixture(t)
		var heldDuringTUI bool
		f.deps.runTUI = func(string) error {
			second, err := instance.Acquire(f.lockPath)
			heldDuringTUI = errors.Is(err, instance.ErrAlreadyRunning)
			if err == nil {
				_ = second.Release()
			}
			return nil
		}
		var stderr bytes.Buffer

		// when
		code := run(nil, &stderr, f.deps)

		// then
		if code != 0 {
			t.Fatalf("code = %d, want 0", code)
		}
		if stderr.Len() != 0 {
			t.Fatalf("stderr = %q, want empty", stderr.String())
		}
		if !heldDuringTUI {
			t.Fatal("lock must be held while the TUI runs")
		}
		assertLockReleased(t, f.lockPath)
	})

	t.Run("TUI를 실행하기 직전에 kube 로그를 끈다", func(t *testing.T) {
		t.Parallel()

		// given
		f := newRunFixture(t)

		// when
		code := run(nil, &bytes.Buffer{}, f.deps)

		// then
		if code != 0 || !reflect.DeepEqual(f.events, []string{"silenceKubeLogs", "runTUI"}) {
			t.Fatalf("code = %d, events = %v, want silenceKubeLogs before runTUI", code, f.events)
		}
	})

	t.Run("사전 검사에 실패하면 kube 로그를 끄지 않는다", func(t *testing.T) {
		t.Parallel()

		// given
		f := newRunFixture(t)
		f.deps.hostsPath = filepath.Join(t.TempDir(), "missing-hosts")

		// when
		code := run(nil, &bytes.Buffer{}, f.deps)

		// then
		if code != 1 || len(f.events) != 0 {
			t.Fatalf("code = %d, events = %v, want 1 and none", code, f.events)
		}
	})

	t.Run("kubeconfig 플래그 값을 TUI에 그대로 넘기고 없으면 빈 문자열을 넘긴다", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			args []string
			want string
		}{
			{"플래그가 없다", nil, ""},
			{"두 인자 형식", []string{"--kubeconfig", "/tmp/kube"}, "/tmp/kube"},
			{"등호 형식", []string{"-kubeconfig=/tmp/other"}, "/tmp/other"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				// given
				f := newRunFixture(t)

				// when
				code := run(tt.args, &bytes.Buffer{}, f.deps)

				// then
				if code != 0 {
					t.Fatalf("code = %d, want 0", code)
				}
				if len(f.tuiCalls) != 1 || f.tuiCalls[0] != tt.want {
					t.Fatalf("TUI calls = %v, want [%q]", f.tuiCalls, tt.want)
				}
			})
		}
	})

	t.Run("알 수 없는 플래그면 사용법을 출력하고 2를 반환하며 아무것도 실행하지 않는다", func(t *testing.T) {
		t.Parallel()

		// given
		f := newRunFixture(t)
		var stderr bytes.Buffer

		// when
		code := run([]string{"--bogus"}, &stderr, f.deps)

		// then
		if code != 2 {
			t.Fatalf("code = %d, want 2", code)
		}
		if !strings.Contains(stderr.String(), "flag provided but not defined: -bogus") || !strings.Contains(stderr.String(), "-kubeconfig") {
			t.Fatalf("stderr = %q, want parse error and usage", stderr.String())
		}
		if len(f.tuiCalls) != 0 {
			t.Fatalf("TUI must not run, calls = %v", f.tuiCalls)
		}
	})

	t.Run("-h 플래그면 사용법을 출력하고 0을 반환하며 아무것도 실행하지 않는다", func(t *testing.T) {
		t.Parallel()

		// given
		f := newRunFixture(t)
		var stderr bytes.Buffer

		// when
		code := run([]string{"-h"}, &stderr, f.deps)

		// then
		if code != 0 {
			t.Fatalf("code = %d, want 0", code)
		}
		if !strings.Contains(stderr.String(), "Usage of kube-service-tunnel") || !strings.Contains(stderr.String(), "Path to kubeconfig file") {
			t.Fatalf("stderr = %q, want usage", stderr.String())
		}
		if len(f.tuiCalls) != 0 {
			t.Fatalf("TUI must not run, calls = %v", f.tuiCalls)
		}
	})
}

func TestCheckHostsFilePermission(t *testing.T) {
	t.Parallel()

	t.Run("쓰기 가능한 파일이면 nil을 반환한다", func(t *testing.T) {
		t.Parallel()

		// given
		path := filepath.Join(t.TempDir(), "hosts")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatalf("write hosts: %v", err)
		}

		// when
		err := checkHostsFilePermission(path)

		// then
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("파일이 없으면 경로와 sudo 안내가 담긴 오류를 반환한다", func(t *testing.T) {
		t.Parallel()

		// given
		path := filepath.Join(t.TempDir(), "missing")

		// when
		err := checkHostsFilePermission(path)

		// then
		if err == nil || !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "cannot write to "+path) || !strings.Contains(err.Error(), "sudo") {
			t.Fatalf("error = %v, want wrapped not-exist error with sudo hint", err)
		}
	})
}

func TestDefaultDeps(t *testing.T) {
	t.Parallel()

	t.Run("기본 의존성은 /etc/hosts와 기본 락 경로를 가리킨다", func(t *testing.T) {
		t.Parallel()

		// given
		// when
		deps := defaultDeps()

		// then
		if deps.hostsPath != "/etc/hosts" || deps.lockPath != instance.DefaultLockPath || deps.acquire == nil || deps.runTUI == nil || deps.silenceKubeLogs == nil || deps.name == "" {
			t.Fatalf("unexpected defaults: %+v", deps)
		}
	})
}

// TestSilenceKubeLogs stays serial on purpose: it swaps the process-wide os.Stderr and klog
// global state, so it must not overlap with any parallel test in this package.
func TestSilenceKubeLogs(t *testing.T) {
	t.Run("끄기 전에는 klog 오류가 프로세스 stderr에 쓰이고 끈 뒤에는 아무것도 쓰이지 않는다", func(t *testing.T) {
		// given
		original := os.Stderr
		t.Cleanup(func() {
			os.Stderr = original
			klog.ClearLogger()
			klog.LogToStderr(true)
		})
		klog.LogToStderr(true)
		before := captureStderr(t, func() { klog.Error("before silence") })

		// when
		silenceKubeLogs()
		after := captureStderr(t, func() {
			klog.Error("after silence")
			klog.ErrorS(errors.New("boom"), "after silence")
			klog.Flush()
		})

		// then
		if !strings.Contains(before, "before silence") {
			t.Fatalf("control stderr = %q, want klog output before silencing", before)
		}
		if after != "" {
			t.Fatalf("stderr after silencing = %q, want empty", after)
		}
	})
}

// captureStderr returns what write sent to the process stderr.
func captureStderr(t *testing.T, write func()) string {
	t.Helper()
	original := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = writer
	defer func() { os.Stderr = original }()

	write()
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	captured, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	return string(captured)
}

func assertLockReleased(t *testing.T, path string) {
	t.Helper()
	lock, err := instance.Acquire(path)
	if err != nil {
		t.Fatalf("lock still held: %v", err)
	}
	_ = lock.Release()
}

type failingRelease struct{}

func (failingRelease) Release() error { return errors.New("release lock: close failed") }

func releaseOrFail(t *testing.T, lock releaser) {
	t.Helper()
	if err := lock.Release(); err != nil {
		t.Errorf("release lock: %v", err)
	}
}
