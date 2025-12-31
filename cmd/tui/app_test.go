package tui

import (
	"context"
	"testing"
	"time"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const eventLoopTimeout = 2 * time.Second

type stubManager struct{}

func (stubManager) GetAllDNSTunnels() []dns.DNSTunnel { return nil }

func (stubManager) RegisterAllByContext(context.Context, string, []string) (dns.BulkRegistrationResult, error) {
	return dns.BulkRegistrationResult{}, nil
}

func (stubManager) RegisterDNSTunnel(context.Context, string, string, string) error { return nil }

func (stubManager) UnregisterDNSTunnel(string) error { return nil }

func (stubManager) SetTunnelLostHandler(func(dns.DNSTunnel, error)) {}

func (stubManager) Cleanup() error { return nil }

type runningApp struct {
	app    *App
	screen tcell.SimulationScreen
	done   chan error
}

func startTestApp(t *testing.T) *runningApp {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	app := &App{
		app:     tview.NewApplication(),
		manager: stubManager{},
		store:   store.NewStore(),
		ctx:     ctx,
		cancel:  cancel,
	}
	app.setupUI()
	app.app.SetRoot(app.pages, true)
	app.app.SetInputCapture(app.handleGlobalInput)

	screen := tcell.NewSimulationScreen("UTF-8")
	app.app.SetScreen(screen)

	running := &runningApp{app: app, screen: screen, done: make(chan error)}
	go func() {
		_ = app.app.Run()
		close(running.done)
	}()
	t.Cleanup(func() {
		app.app.Stop()
		waitFor(running.done, eventLoopTimeout)
	})

	if !eventLoopResponds(app.app) {
		t.Fatal("event loop did not start")
	}
	return running
}

func eventLoopResponds(app *tview.Application) bool {
	processed := make(chan error, 1)
	go func() {
		app.QueueUpdate(func() {})
		processed <- nil
	}()
	return waitFor(processed, eventLoopTimeout)
}

func waitFor(ch <-chan error, timeout time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(timeout):
		return false
	}
}

func messageEventually(s *store.Store, want string) bool {
	deadline := time.Now().Add(eventLoopTimeout)
	for time.Now().Before(deadline) {
		if s.GetState().Message == want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestServiceEnterKeepsEventLoopAlive(t *testing.T) {
	t.Run("네임스페이스가 없을 때 Services 창에서 Enter를 눌러도 UI가 멈추지 않는다", func(t *testing.T) {
		// given
		running := startTestApp(t)
		running.app.app.QueueUpdate(func() { running.app.app.SetFocus(running.app.mainView) })

		// when
		running.screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)

		// then
		if !messageEventually(running.app.store, "Please select a namespace first") {
			t.Fatalf("message = %q, want namespace hint", running.app.store.GetState().Message)
		}
		if !eventLoopResponds(running.app.app) {
			t.Fatal("event loop is blocked after Enter on the Services view")
		}
	})
}

func TestCtrlCQuitsWhileLoading(t *testing.T) {
	t.Run("로딩 중에도 Ctrl+C를 누르면 앱이 종료된다", func(t *testing.T) {
		// given
		running := startTestApp(t)
		running.app.store.SetLoading(true)

		// when
		running.screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModCtrl)

		// then
		if !waitFor(running.done, eventLoopTimeout) {
			t.Fatal("app did not stop after Ctrl+C while loading")
		}
	})
}
