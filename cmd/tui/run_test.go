package tui

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/clipboard"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
	"github.com/gdamore/tcell/v2"
)

// runHarness wires run to fakes and a simulation screen and runs it on its own goroutine.
type runHarness struct {
	manager      *fakeManager
	kube         *fakeKube
	screen       tcell.SimulationScreen
	signals      chan os.Signal
	stopped      atomic.Int32
	receivedPath chan string
	initialized  chan struct{}
	result       chan error
}

func startRun(t *testing.T, manager *fakeManager) *runHarness {
	t.Helper()
	h := &runHarness{
		manager:      manager,
		kube:         &fakeKube{current: "dev", resources: sampleCluster()},
		screen:       tcell.NewSimulationScreen("UTF-8"),
		signals:      make(chan os.Signal, 1),
		receivedPath: make(chan string, 2),
		initialized:  make(chan struct{}),
		result:       make(chan error, 1),
	}
	deps := runDeps{
		newKubeAdapter: func(path string) (kube.KubeAdapterInterface, error) {
			h.receivedPath <- path
			return h.kube, nil
		},
		newManager: func(string) (dns.DNSManagerInterface, error) { return h.manager, nil },
		copier:     &fakeCopier{},
		watchSignals: func() (<-chan os.Signal, func()) {
			close(h.initialized)
			return h.signals, func() { h.stopped.Add(1) }
		},
		screen: h.screen,
	}
	go func() { h.result <- run("/tmp/kubeconfig", deps) }()
	return h
}

// waitRunning waits until the event loop has run the update that follows startup discovery. The initial tunnel
// listing happens in newApp, so a second listing can only come from the loop, which then reads input and signals.
func (h *runHarness) waitRunning(t *testing.T) {
	t.Helper()
	select {
	case <-h.initialized:
	case <-time.After(eventLoopTimeout):
		t.Fatal("run did not initialize the screen")
	}
	eventually(t, "event loop to run", func() bool { return h.manager.listCalls() >= 2 })
}

func (h *runHarness) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-h.result:
		return err
	case <-time.After(eventLoopTimeout):
		t.Fatal("run did not return")
		return nil
	}
}

func TestRunStartup(t *testing.T) {
	t.Parallel()
	t.Run("kube 어댑터를 만들지 못하면 매니저를 만들지 않고 오류를 돌려준다", func(t *testing.T) {
		t.Parallel()
		// given
		adapterErr := errors.New("no kubeconfig")
		managerCreated := false
		deps := runDeps{
			newKubeAdapter: func(string) (kube.KubeAdapterInterface, error) { return nil, adapterErr },
			newManager: func(string) (dns.DNSManagerInterface, error) {
				managerCreated = true
				return &fakeManager{}, nil
			},
		}

		// when
		err := run("/tmp/kubeconfig", deps)

		// then
		if !errors.Is(err, adapterErr) || err.Error() != "create kube adapter: no kubeconfig" {
			t.Fatalf("err = %v", err)
		}
		if managerCreated {
			t.Fatal("manager must not be created after the adapter failed")
		}
	})

	t.Run("매니저를 만들지 못하면 오류를 돌려주고 신호를 구독하지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		managerErr := errors.New("no root")
		watched := false
		deps := runDeps{
			newKubeAdapter: func(string) (kube.KubeAdapterInterface, error) { return &fakeKube{}, nil },
			newManager:     func(string) (dns.DNSManagerInterface, error) { return nil, managerErr },
			watchSignals: func() (<-chan os.Signal, func()) {
				watched = true
				return nil, func() {}
			},
		}

		// when
		err := run("/tmp/kubeconfig", deps)

		// then
		if !errors.Is(err, managerErr) || err.Error() != "create service tunnel manager: no root" {
			t.Fatalf("err = %v", err)
		}
		if watched {
			t.Fatal("signals must not be watched when startup failed")
		}
	})

	t.Run("kubeconfig 경로를 어댑터에 넘기고 시작하면 컨텍스트 탐색을 시작한다", func(t *testing.T) {
		t.Parallel()
		// given
		h := startRun(t, &fakeManager{})
		h.waitRunning(t)

		// when
		h.screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModCtrl)
		err := h.wait(t)

		// then
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got := <-h.receivedPath; got != "/tmp/kubeconfig" {
			t.Fatalf("kubeconfig path = %q", got)
		}
		eventually(t, "discovery to run", func() bool { return h.kube.fetchAllCalls() == 1 })
	})
}

func TestRunShutdown(t *testing.T) {
	t.Parallel()
	t.Run("Ctrl+C로 종료하면 매니저를 정리하고 신호 구독을 풀며 오류가 없다", func(t *testing.T) {
		t.Parallel()
		// given
		h := startRun(t, &fakeManager{})
		h.waitRunning(t)

		// when
		h.screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModCtrl)
		err := h.wait(t)

		// then
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got := h.manager.cleanupCalls(); got != 1 {
			t.Fatalf("cleanup calls = %d, want 1", got)
		}
		if got := h.stopped.Load(); got != 1 {
			t.Fatalf("signal stop calls = %d, want 1", got)
		}
	})

	t.Run("종료 신호를 받으면 화면 입력 없이도 종료하고 매니저를 정리한다", func(t *testing.T) {
		t.Parallel()
		// given
		h := startRun(t, &fakeManager{})
		h.waitRunning(t)

		// when
		h.signals <- syscall.SIGTERM
		err := h.wait(t)

		// then
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got := h.manager.cleanupCalls(); got != 1 {
			t.Fatalf("cleanup calls = %d, want 1", got)
		}
	})

	t.Run("정리에 실패하면 cleanup 오류로 감싸서 돌려준다", func(t *testing.T) {
		t.Parallel()
		// given
		cleanupErr := errors.New("forward still open")
		h := startRun(t, &fakeManager{cleanupErr: cleanupErr})
		h.waitRunning(t)

		// when
		h.signals <- os.Interrupt
		err := h.wait(t)

		// then
		if !errors.Is(err, cleanupErr) || err.Error() != "cleanup: forward still open" {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestQuitOnSignal(t *testing.T) {
	t.Parallel()
	t.Run("컨텍스트가 먼저 끝나면 신호를 기다리지 않고 앱도 멈추지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startTestApp(t)
		returned := make(chan error, 1)
		go func() {
			running.app.quitOnSignal(make(chan os.Signal))
			returned <- nil
		}()

		// when
		running.app.cancel()

		// then
		if !waitFor(returned, eventLoopTimeout) {
			t.Fatal("quitOnSignal did not return after the context ended")
		}
		if !eventLoopResponds(running.app.app) {
			t.Fatal("app stopped without a signal")
		}
		select {
		case err := <-running.done:
			t.Fatalf("app stopped without a signal: %v", err)
		default:
		}
	})

	t.Run("신호를 받으면 앱을 멈추고 컨텍스트를 취소한다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startTestApp(t)
		signals := make(chan os.Signal, 1)
		go running.app.quitOnSignal(signals)

		// when
		signals <- syscall.SIGHUP

		// then
		if !waitFor(running.done, eventLoopTimeout) {
			t.Fatal("app did not stop after the signal")
		}
		if running.app.ctx.Err() == nil {
			t.Fatal("context must be canceled after the signal")
		}
	})
}

func TestNewAppDefaults(t *testing.T) {
	t.Parallel()
	t.Run("복사기를 주지 않으면 시스템 클립보드를 쓴다", func(t *testing.T) {
		t.Parallel()
		// given
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		// when
		app := newApp(ctx, cancel, dependencies{manager: &fakeManager{}, kubeAdapter: &fakeKube{}, store: store.NewStore()})

		// then
		if _, ok := app.copier.(*clipboard.System); !ok {
			t.Fatalf("copier = %T, want *clipboard.System", app.copier)
		}
	})
}

// The signal test is not parallel: delivering a signal to the test process affects every goroutine in it.
func TestNotifyShutdownSignals(t *testing.T) {
	t.Run("SIGHUP을 받으면 신호 채널로 전달한다", func(t *testing.T) {
		// given
		signals, stop := notifyShutdownSignals()
		self, err := os.FindProcess(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}

		// when
		if err := self.Signal(syscall.SIGHUP); err != nil {
			t.Fatal(err)
		}

		// then
		select {
		case got := <-signals:
			if got != syscall.SIGHUP {
				t.Fatalf("signal = %v, want SIGHUP", got)
			}
		case <-time.After(eventLoopTimeout):
			t.Fatal("signal was not delivered")
		}
		stop()
	})
}
