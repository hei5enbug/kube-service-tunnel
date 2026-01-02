package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/clipboard"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const mainPage = "main"

// App owns the widgets. Fields below the services are touched only on the UI goroutine.
type App struct {
	app       *tview.Application
	screenRef screenRef

	store       *store.Store
	manager     dns.DNSManagerInterface
	kubeAdapter kube.KubeAdapterInterface
	copier      clipboard.Copier

	ctx    context.Context
	cancel context.CancelFunc

	pages       *tview.Pages
	body        *tview.Flex
	left        *tview.Flex
	statusRow   *tview.Flex
	header      *tview.TextView
	status      *tview.TextView
	hints       *tview.TextView
	filterInput *tview.InputField
	panes       []*pane

	theme      Theme
	themeIndex int
	layout     layout
	filterPane store.FocusArea
	modal      *openModal
	loading    *loadingView

	tunnels       []dns.DNSTunnel
	contextRows   []contextRow
	namespaceRows []namespaceRow
	serviceRows   []serviceRow
	tunnelRows    []dns.DNSTunnel
}

type dependencies struct {
	manager     dns.DNSManagerInterface
	kubeAdapter kube.KubeAdapterInterface
	copier      clipboard.Copier
	store       *store.Store
	screen      tcell.Screen
}

// screenRef holds the screen seen by the last draw, for the OSC 52 fallback that runs off the UI goroutine.
type screenRef struct {
	mu     sync.Mutex
	screen tcell.Screen
}

func (r *screenRef) set(screen tcell.Screen) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.screen = screen
}

func (r *screenRef) get() tcell.Screen {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.screen
}

func Run(kubeconfigPath string) (runErr error) {
	kubeAdapter, err := kube.NewKubeAdapter(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("create kube adapter: %w", err)
	}
	manager, err := dns.NewDNSManager(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("create service tunnel manager: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := newApp(ctx, cancel, dependencies{
		manager:     manager,
		kubeAdapter: kubeAdapter,
		copier:      clipboard.NewSystem(),
		store:       store.NewStore(),
	})

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigChan)
	defer func() {
		cancel()
		if cleanupErr := manager.Cleanup(); cleanupErr != nil {
			runErr = errors.Join(runErr, fmt.Errorf("cleanup: %w", cleanupErr))
		}
	}()

	go app.quitOnSignal(sigChan)
	app.start()
	return app.app.Run()
}

func newApp(ctx context.Context, cancel context.CancelFunc, deps dependencies) *App {
	a := &App{
		app:         tview.NewApplication(),
		store:       deps.store,
		manager:     deps.manager,
		kubeAdapter: deps.kubeAdapter,
		copier:      deps.copier,
		ctx:         ctx,
		cancel:      cancel,
		theme:       themes[0],
	}
	if a.copier == nil {
		a.copier = clipboard.NewSystem()
	}
	a.setupUI()
	a.app.SetRoot(a.pages, true)
	a.app.SetInputCapture(a.handleGlobalKey)
	a.app.SetBeforeDrawFunc(a.beforeDraw)
	if deps.screen != nil {
		a.app.SetScreen(deps.screen)
	}
	a.focusPane(store.FocusContexts)
	a.refreshTunnelViews()
	a.manager.SetTunnelLostHandler(a.onTunnelLost)
	a.subscribe()
	return a
}

func (a *App) setupUI() {
	a.panes = []*pane{
		a.newPane(store.FocusContexts, 0),
		a.newPane(store.FocusNamespaces, 0),
		a.newPane(store.FocusServices, 1),
		a.newPane(store.FocusTunnels, 1),
	}
	a.header = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	a.status = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	a.hints = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	a.filterInput = a.newFilterInput()
	a.body = tview.NewFlex()
	a.left = tview.NewFlex().SetDirection(tview.FlexRow)
	a.statusRow = tview.NewFlex().AddItem(a.status, 0, 1, false)

	root := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.header, 1, 0, false).
		AddItem(a.body, 0, 1, false).
		AddItem(a.statusRow, 1, 0, false).
		AddItem(a.hints, 1, 0, false)
	a.pages = tview.NewPages().AddPage(mainPage, root, true, true)
	a.applyTheme()
}

func (a *App) quitOnSignal(sigChan <-chan os.Signal) {
	select {
	case <-sigChan:
		a.Quit()
	case <-a.ctx.Done():
	}
}

func (a *App) Quit() {
	a.cancel()
	a.app.Stop()
}
