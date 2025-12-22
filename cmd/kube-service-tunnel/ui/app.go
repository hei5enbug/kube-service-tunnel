package ui

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/byoungmin/kube-service-tunnel/cmd/kube-service-tunnel/dns"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

var backgroundColor = tcell.NewRGBColor(0, 0, 0)
var textColor = tcell.ColorWhite

type App struct {
	app         *tview.Application
	header      *tview.Flex
	contextList *tview.List
	sidebar     *tview.List
	mainView    *tview.Table
	dnsView     *tview.Table
	helpView    *tview.TextView
	messageView *tview.TextView
	root        *tview.Flex
	manager     *dns.DnsManager
	isLoading   bool
}

func Run(kubeconfigPath string, bgColorName string, textColorName string) error {
	bgColor := parseColor(bgColorName)
	if bgColor == tcell.ColorDefault {
		bgColor = tcell.NewRGBColor(0, 0, 0)
	}
	
	textCol := parseColor(textColorName)
	if textCol == tcell.ColorDefault {
		textCol = tcell.ColorWhite
	}
	
	backgroundColor = bgColor
	textColor = textCol
	
	tview.Styles.PrimitiveBackgroundColor = backgroundColor
	tview.Styles.ContrastBackgroundColor = backgroundColor
	tview.Styles.MoreContrastBackgroundColor = backgroundColor
	tview.Styles.PrimaryTextColor = textColor
	tview.Styles.SecondaryTextColor = textColor

	manager, err := dns.NewDnsManager(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("create service tunnel manager: %w", err)
	}

	if err := manager.CleanupHostsFile(); err != nil {
		return fmt.Errorf("cleanup hosts file on startup: %w", err)
	}

	app := &App{
		app:     tview.NewApplication(),
		manager: manager,
	}

	app.setupUI()
	app.app.SetRoot(app.root, true).SetFocus(app.contextList)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigChan
		app.manager.Cleanup()
		app.app.Stop()
		os.Exit(0)
	}()

	defer func() {
		if r := recover(); r != nil {
			app.manager.Cleanup()
			panic(r)
		}
		app.manager.Cleanup()
	}()

	app.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if app.isLoading {
			if event.Key() == tcell.KeyCtrlC {
				app.manager.Cleanup()
				app.app.Stop()
				os.Exit(0)
			}
			return nil
		}
		if event.Key() == tcell.KeyCtrlC {
			app.manager.Cleanup()
			app.app.Stop()
			os.Exit(0)
		}
		return event
	})

	runErr := app.app.Run()
	app.manager.Cleanup()
	return runErr
}

func (a *App) setupUI() {
	a.sidebar = renderSidebar(a)
	a.mainView = renderMainView(a)
	a.dnsView = renderDNSView(a)
	
	a.messageView = renderMessageView(a)
	
	a.header = renderHeader(a)

	servicesAndDNS := tview.NewFlex().
		AddItem(a.mainView, 0, 4, false).
		AddItem(a.dnsView, 0, 9, false)
	servicesAndDNS.SetBackgroundColor(backgroundColor)

	content := tview.NewFlex().
		AddItem(a.sidebar, 0, 7, true).
		AddItem(servicesAndDNS, 0, 30, false)
	content.SetBackgroundColor(backgroundColor)

	a.root = tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(a.header, 0, 1, true).
		AddItem(content, 0, 1, false)
	a.root.SetBackgroundColor(backgroundColor)

	if err := a.manager.RefreshNamespaces(); err != nil {
		a.setMessage(fmt.Sprintf("Error loading namespaces: %v", err))
	} else {
		a.updateSidebar()
	}
	if err := a.manager.RefreshDNSEntries(); err != nil {
	}
	a.updateDNSView()
}

