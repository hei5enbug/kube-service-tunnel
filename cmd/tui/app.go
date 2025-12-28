package tui

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

var backgroundColor = tcell.NewRGBColor(0, 0, 0)
var textColor = tcell.ColorWhite
var focusedBorderColor = tcell.ColorGreen

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
	manager       dns.DNSManagerInterface

	kubeAdapter       kube.KubeAdapterInterface
	selectedContext   string
	selectedNamespace string
	contexts          []kube.Context
	namespaces        []string
	services          []kube.Service

	ctx    context.Context
	cancel context.CancelFunc

	isLoading bool
}

func Run(kubeconfigPath string) error {
	backgroundColor = tcell.NewRGBColor(0, 0, 0)
	textColor = tcell.ColorWhite

	tview.Styles.PrimitiveBackgroundColor = backgroundColor
	tview.Styles.ContrastBackgroundColor = backgroundColor
	tview.Styles.MoreContrastBackgroundColor = backgroundColor
	tview.Styles.PrimaryTextColor = textColor
	tview.Styles.SecondaryTextColor = textColor

	manager, err := dns.NewDNSManager(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("create service tunnel manager: %w", err)
	}

	kubeAdapter, err := kube.NewKubeAdapter(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("create kube adapter: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app := &App{
		app:         tview.NewApplication(),
		manager:     manager,
		kubeAdapter: kubeAdapter,
		ctx:         ctx,
		cancel:      cancel,
	}

	initialCtx, initialCancel := context.WithTimeout(ctx, 10*time.Second)
	defer initialCancel()

	contexts, err := kubeAdapter.ListContexts(initialCtx)
	if err != nil {
		return fmt.Errorf("list contexts: %w", err)
	}
	app.contexts = contexts
	if len(contexts) > 0 {
		app.selectedContext = contexts[0].Name
	}

	app.setupUI()
	app.app.SetRoot(app.pages, true).SetFocus(app.contextList)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigChan
		app.cancel()
		app.manager.Cleanup()
		app.app.Stop()
		os.Exit(0)
	}()

	defer func() {
		if r := recover(); r != nil {
			app.cancel()
			app.manager.Cleanup()
			panic(r)
		}
		app.cancel()
		app.manager.Cleanup()
	}()

	app.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if app.isLoading {
			if event.Key() == tcell.KeyCtrlC {
				app.cancel()
				app.manager.Cleanup()
				app.app.Stop()
				os.Exit(0)
			}
			return nil
		}
		if event.Key() == tcell.KeyCtrlC {
			app.cancel()
			app.manager.Cleanup()
			app.app.Stop()
			os.Exit(0)
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
	})

	runErr := app.app.Run()
	app.cancel()
	app.manager.Cleanup()
	return runErr
}

func (a *App) setupUI() {
	a.messageView = a.RenderMessageView()
	a.helpView = a.RenderHelpView()
	a.contextList = a.RenderContextView()
	a.namespaceView = a.RenderNamespaceView()
	a.mainView = a.RenderServiceView()
	a.dnsView = a.RenderTunnelView()
	a.header = a.RenderHeader()

	mainFlex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.header, 0, 2, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexColumn).
			AddItem(a.namespaceView, 0, 1, true).
			AddItem(a.mainView, 0, 2, false).
			AddItem(a.dnsView, 0, 2, false), 0, 4, true)

	a.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(mainFlex, 0, 1, true)
	a.root.SetBackgroundColor(backgroundColor)

	a.pages = tview.NewPages().
		AddPage("main", a.root, true, true)

	if err := a.RefreshNamespaces(); err != nil {
		a.SetMessage(fmt.Sprintf("Error loading namespaces: %v", err))
	} else {
		a.UpdateNamespaceView()
	}
	a.UpdateDNSView()
	a.UpdateContextList()
}
