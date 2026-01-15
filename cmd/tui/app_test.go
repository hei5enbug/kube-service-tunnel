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

const (
	eventLoopTimeout = 5 * time.Second
	pollInterval     = time.Millisecond
)

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

type runningApp struct {
	app     *App
	screen  tcell.SimulationScreen
	manager *fakeManager
	kube    *fakeKube
	copier  *fakeCopier
	done    chan error
	ticks   chan time.Time
	flushed chan struct{}
}

// flushKey is consumed by the input capture that startTestApp wraps. Keys reach the event loop in order, so
// once the capture sees it, every key injected before it has been handled.
const flushKey = tcell.KeyF24

func startTestApp(t *testing.T, options ...testOption) *runningApp {
	t.Helper()
	config := testConfig{manager: &fakeManager{}, kube: &fakeKube{}, copier: &fakeCopier{}, width: 100, height: 30}
	for _, option := range options {
		option(&config)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ticks := make(chan time.Time)
	screen := tcell.NewSimulationScreen("UTF-8")
	app := newApp(ctx, cancel, dependencies{
		manager:     config.manager,
		kubeAdapter: config.kube,
		copier:      config.copier,
		store:       store.NewStore(config.storeOptions...),
		screen:      screen,
		frameTicker: func() (<-chan time.Time, func()) { return ticks, func() {} },
	})
	screen.SetSize(config.width, config.height)

	running := &runningApp{
		app: app, screen: screen, manager: config.manager, kube: config.kube, copier: config.copier,
		done: make(chan error), ticks: ticks, flushed: make(chan struct{}, 16),
	}
	globalKey := app.app.GetInputCapture()
	app.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == flushKey {
			running.flushed <- struct{}{}
			return nil
		}
		return globalKey(event)
	})
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

func waitFor[T any](ch <-chan T, timeout time.Duration) bool {
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

// eventually polls condition until it holds. The timeout only bounds a failing test; a passing one returns as
// soon as the condition holds.
func eventually(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(eventLoopTimeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(pollInterval)
	}
}

// settle waits until every key injected so far has been handled and every goroutine that a handler started has
// returned, so that a test can assert that nothing happened without waiting a fixed time. It closes the given
// gates first, which lets work that waits on a gate finish.
func (r *runningApp) settle(t *testing.T, release ...chan struct{}) {
	t.Helper()
	r.screen.InjectKey(flushKey, 0, tcell.ModNone)
	select {
	case <-r.flushed:
	case <-time.After(eventLoopTimeout):
		t.Fatal("event loop did not handle the injected keys")
	}
	for _, gate := range release {
		close(gate)
	}
	finished := make(chan error, 1)
	go func() {
		r.app.work.Wait()
		finished <- nil
	}()
	if !waitFor(finished, eventLoopTimeout) {
		t.Fatal("background work did not finish")
	}
}

// tick advances the loading animation by one frame.
func (r *runningApp) tick(t *testing.T) {
	t.Helper()
	select {
	case r.ticks <- time.Time{}:
	case <-time.After(eventLoopTimeout):
		t.Fatal("loading animation did not take the tick")
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
	t.Parallel()
	t.Run("네임스페이스가 없을 때 Services 창에서 Enter를 눌러도 UI가 멈추지 않는다", func(t *testing.T) {
		t.Parallel()
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
	t.Parallel()
	t.Run("로딩 중에도 Ctrl+C를 누르면 앱이 종료된다", func(t *testing.T) {
		t.Parallel()
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
