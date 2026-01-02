package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const eventLoopTimeout = 5 * time.Second

type testConfig struct {
	manager      *fakeManager
	kube         *fakeKube
	copier       *fakeCopier
	storeOptions []store.Option
	width        int
	height       int
}

type testOption func(*testConfig)

func withManager(manager *fakeManager) testOption {
	return func(c *testConfig) { c.manager = manager }
}

func withKube(k *fakeKube) testOption {
	return func(c *testConfig) { c.kube = k }
}

func withCopier(copier *fakeCopier) testOption {
	return func(c *testConfig) { c.copier = copier }
}

func withStore(options ...store.Option) testOption {
	return func(c *testConfig) { c.storeOptions = options }
}

func withSize(width, height int) testOption {
	return func(c *testConfig) { c.width, c.height = width, height }
}

type runningApp struct {
	app     *App
	screen  tcell.SimulationScreen
	manager *fakeManager
	kube    *fakeKube
	copier  *fakeCopier
	done    chan error
}

func startTestApp(t *testing.T, options ...testOption) *runningApp {
	t.Helper()
	config := testConfig{manager: &fakeManager{}, kube: &fakeKube{}, copier: &fakeCopier{}, width: 100, height: 30}
	for _, option := range options {
		option(&config)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	screen := tcell.NewSimulationScreen("UTF-8")
	app := newApp(ctx, cancel, dependencies{
		manager:     config.manager,
		kubeAdapter: config.kube,
		copier:      config.copier,
		store:       store.NewStore(config.storeOptions...),
		screen:      screen,
	})
	screen.SetSize(config.width, config.height)

	running := &runningApp{app: app, screen: screen, manager: config.manager, kube: config.kube, copier: config.copier, done: make(chan error)}
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

// startLoadedApp runs startup discovery against the fake cluster and waits until the UI is idle.
func startLoadedApp(t *testing.T, options ...testOption) *runningApp {
	t.Helper()
	options = append([]testOption{withKube(&fakeKube{current: "dev", resources: sampleCluster()})}, options...)
	running := startTestApp(t, options...)
	running.app.start()
	running.waitIdle(t)
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

// onUI runs fn on the UI goroutine and waits for it.
func (r *runningApp) onUI(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		r.app.app.QueueUpdateDraw(fn)
		done <- nil
	}()
	if !waitFor(done, eventLoopTimeout) {
		t.Fatal("UI goroutine did not run the update")
	}
}

func uiValue[T any](t *testing.T, r *runningApp, fn func() T) T {
	t.Helper()
	var value T
	r.onUI(t, func() { value = fn() })
	return value
}

func eventually(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(eventLoopTimeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *runningApp) waitIdle(t *testing.T) {
	t.Helper()
	eventually(t, "loading to finish", func() bool {
		return !r.app.store.GetState().IsLoading && uiValue(t, r, func() bool { return r.app.loading == nil })
	})
}

func (r *runningApp) press(key tcell.Key) {
	r.screen.InjectKey(key, 0, tcell.ModNone)
}

func (r *runningApp) typeText(text string) {
	for _, char := range text {
		r.screen.InjectKey(tcell.KeyRune, char, tcell.ModNone)
	}
}

func (r *runningApp) focusIs(t *testing.T, area store.FocusArea) bool {
	return uiValue(t, r, func() bool { return r.app.app.GetFocus() == r.app.pane(area).table })
}

func (r *runningApp) waitFocus(t *testing.T, area store.FocusArea) {
	t.Helper()
	eventually(t, "focus on "+string(area), func() bool { return r.focusIs(t, area) })
}

// statusText returns the first line of the status event, which is what the status line shows.
func (r *runningApp) statusText() string {
	first, _, _ := strings.Cut(r.app.store.GetState().Status.Text, "\n")
	return first
}

func (r *runningApp) waitStatus(t *testing.T, want string) {
	t.Helper()
	eventually(t, "status "+want, func() bool { return r.statusText() == want })
}

func (r *runningApp) cursorKey(t *testing.T, area store.FocusArea) string {
	return uiValue(t, r, func() string {
		p := r.app.pane(area)
		index, ok := p.selectedIndex()
		if !ok {
			return ""
		}
		return p.keys[index]
	})
}

func (r *runningApp) moveCursorTo(t *testing.T, area store.FocusArea, key string) {
	t.Helper()
	r.onUI(t, func() {
		p := r.app.pane(area)
		for index, candidate := range p.keys {
			if candidate == key {
				p.table.Select(p.offset+index, 0)
			}
		}
	})
	if got := r.cursorKey(t, area); got != key {
		t.Fatalf("cursor = %q, want %q", got, key)
	}
}

func TestServiceEnterKeepsEventLoopAlive(t *testing.T) {
	t.Run("네임스페이스가 없을 때 Services 창에서 Enter를 눌러도 UI가 멈추지 않는다", func(t *testing.T) {
		// given
		running := startTestApp(t)
		running.app.app.QueueUpdate(func() { running.app.app.SetFocus(running.app.pane(store.FocusServices).table) })

		// when
		running.screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)

		// then
		eventually(t, "namespace hint", func() bool { return running.statusText() == "Please select a namespace first" })
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

func serviceKey(namespace, name string) string {
	return namespace + "/" + name
}

var _ kube.KubeAdapterInterface = (*fakeKube)(nil)

// screenLines returns the drawn screen as text, one string per row. It reads the simulation buffer on the UI
// goroutine because drawing writes it there.
func (r *runningApp) screenLines(t *testing.T) []string {
	t.Helper()
	r.onUI(t, func() {})
	return uiValue(t, r, func() []string {
		cells, width, height := r.screen.GetContents()
		lines := make([]string, height)
		for y := range height {
			row := make([]rune, 0, width)
			for x := range width {
				cell := cells[y*width+x]
				if len(cell.Runes) == 0 {
					row = append(row, ' ')
					continue
				}
				row = append(row, cell.Runes[0])
			}
			lines[y] = string(row)
		}
		return lines
	})
}
