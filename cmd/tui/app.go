package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

var backgroundColor = tcell.NewRGBColor(0, 0, 0)
var textColor = tcell.ColorWhite
var focusedBorderColor = tcell.ColorGreen
var systemColor = tcell.NewRGBColor(255, 255, 0)

type App struct {
	app           *tview.Application
	header        *tview.Flex
	contextList   *tview.Table
	namespaceView *tview.Table
	mainView      *tview.Table
	dnsView       *tview.Table
	helpView      *tview.TextView
	messageView   *tview.TextView
	root          *tview.Flex
	pages         *tview.Pages

	store       *store.Store
	manager     dns.DNSManagerInterface
	kubeAdapter kube.KubeAdapterInterface

	ctx    context.Context
	cancel context.CancelFunc

	loadingStop chan struct{}

	renderedTunnels []dns.DNSTunnel
}

func Run(kubeconfigPath string) (runErr error) {
	backgroundColor = tcell.NewRGBColor(0, 0, 0)
	textColor = tcell.ColorWhite

	tview.Styles.PrimitiveBackgroundColor = backgroundColor
	tview.Styles.ContrastBackgroundColor = backgroundColor
	tview.Styles.MoreContrastBackgroundColor = backgroundColor
	tview.Styles.PrimaryTextColor = textColor
	tview.Styles.SecondaryTextColor = textColor

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

	app := &App{
		app:         tview.NewApplication(),
		manager:     manager,
		kubeAdapter: kubeAdapter,
		ctx:         ctx,
		cancel:      cancel,
		store:       store.NewStore(),
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigChan)
	defer func() {
		cancel()
		if cleanupErr := manager.Cleanup(); cleanupErr != nil {
			runErr = errors.Join(runErr, fmt.Errorf("cleanup: %w", cleanupErr))
		}
	}()

	app.setupUI()
	app.app.SetRoot(app.pages, true)
	app.app.SetFocus(nil)
	app.app.SetInputCapture(app.handleGlobalInput)
	manager.SetTunnelLostHandler(app.onTunnelLost)

	go app.quitOnSignal(sigChan)
	go func() {
		time.Sleep(100 * time.Millisecond)
		app.fetchAllResources()
	}()

	return app.app.Run()
}

func (app *App) quitOnSignal(sigChan <-chan os.Signal) {
	select {
	case <-sigChan:
		app.Quit()
	case <-app.ctx.Done():
	}
}

func (app *App) onTunnelLost(tunnel dns.DNSTunnel, err error) {
	app.store.SetMessage(fmt.Sprintf("Tunnel lost: %s (%v); press Enter on the service to reconnect", tunnel.DNSURL, err))
	app.app.QueueUpdateDraw(app.UpdateDNSView)
}

func (app *App) Quit() {
	app.cancel()
	app.app.Stop()
}

func (app *App) setupUI() {
	app.messageView = app.RenderMessageView()
	app.helpView = app.RenderHelpView()
	app.contextList = app.RenderContextView()
	app.namespaceView = app.RenderNamespaceView()
	app.mainView = app.RenderServiceView()
	app.dnsView = app.RenderTunnelView()
	app.header = app.RenderHeader()

	mainFlex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(app.header, 0, 2, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexColumn).
			AddItem(app.namespaceView, 0, 1, true).
			AddItem(app.mainView, 0, 2, false).
			AddItem(app.dnsView, 0, 2, false), 0, 4, true)

	app.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(mainFlex, 0, 1, true)
	app.root.SetBackgroundColor(backgroundColor)

	app.pages = tview.NewPages().
		AddPage("main", app.root, true, true)

	app.UpdateDNSView()
	app.UpdateContextList()
	app.SetupLoadingSubscription()
	app.SetupFocusSubscription()
}

func (app *App) SetupFocusSubscription() {
	var prevState store.State
	app.store.Subscribe(func(s store.State) {
		defer func() { prevState = s }()

		focusChanged := s.Focus != prevState.Focus

		if focusChanged {
			if s.Focus == "" {
				app.app.QueueUpdateDraw(func() {
					app.app.SetFocus(nil)
				})
			} else {
				target := app.getWidgetForFocus(s.Focus)
				if target != nil && app.app.GetFocus() != target {
					app.app.QueueUpdateDraw(func() {
						app.app.SetFocus(target)
					})
				} else if target == nil {
					app.app.QueueUpdateDraw(func() {
						app.app.SetFocus(nil)
					})
				}
			}
		}
	})
}

func (app *App) getWidgetForFocus(focus store.FocusArea) tview.Primitive {
	switch focus {
	case store.FocusContexts:
		return app.contextList
	case store.FocusNamespaces:
		return app.namespaceView
	case store.FocusServices:
		return app.mainView
	case store.FocusTunnels:
		return app.dnsView
	default:
		return nil
	}
}

func (app *App) fetchAllResources() {
	app.store.SetLoading(true)
	defer app.store.SetLoading(false)

	resourceMap, err := app.kubeAdapter.FetchAllResources(app.ctx)
	app.store.SetAllResources(resourceMap)
	app.store.SetFocus(store.FocusContexts)

	if err != nil {
		app.store.SetMessage(describeFetchError(err))
		return
	}
	app.store.SetMessage("All resources loaded and cached")
}

func describeFetchError(err error) string {
	count := 1
	var joined interface{ Unwrap() []error }
	if errors.As(err, &joined) {
		count = len(joined.Unwrap())
	}
	firstLine, _, _ := strings.Cut(err.Error(), "\n")
	return fmt.Sprintf("Loaded with errors: %s (%d errors)", firstLine, count)
}

func (app *App) handleGlobalInput(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyCtrlC {
		app.Quit()
		return nil
	}
	if app.store.GetState().IsLoading {
		return nil
	}
	if event.Key() == tcell.KeyCtrlB {
		app.showColorInputModal("Background Color", app.changeBackgroundColor)
		return nil
	}
	if event.Key() == tcell.KeyCtrlT {
		app.showColorInputModal("Text Color", app.changeTextColor)
		return nil
	}
	return event
}
