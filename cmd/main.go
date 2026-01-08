package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/byoungmin/kube-service-tunnel/cmd/tui"
	"github.com/byoungmin/kube-service-tunnel/internal/instance"
	"github.com/go-logr/logr"
	"k8s.io/klog/v2"
)

const (
	hostsPath       = "/etc/hosts"
	exitOK          = 0
	exitFailure     = 1
	exitUsage       = 2
	kubeconfigUsage = "Path to kubeconfig file (default: ~/.kube/config)"
)

type runDeps struct {
	name      string
	hostsPath string
	lockPath  string
	acquire   func(path string) (*instance.Lock, error)
	runTUI    func(kubeconfigPath string) error
	// silenceKubeLogs stops client-go's klog output, which would otherwise corrupt the terminal the TUI draws on.
	silenceKubeLogs func()
}

func defaultDeps() runDeps {
	return runDeps{
		name:      os.Args[0],
		hostsPath: hostsPath,
		lockPath:  instance.DefaultLockPath,
		acquire:   instance.Acquire,
		runTUI:    tui.Run,

		silenceKubeLogs: silenceKubeLogs,
	}
}

func silenceKubeLogs() {
	klog.LogToStderr(false)
	klog.SetOutput(io.Discard)
	// ERROR and above would still reach stderr through klog's stderr threshold; a discarding logger bypasses it.
	klog.SetLogger(logr.Discard())
}

func checkHostsFilePermission(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w\n\nPlease run this program with sudo or ensure you have write permission to /etc/hosts", path, err)
	}
	file.Close()
	return nil
}

func main() {
	os.Exit(run(os.Args[1:], os.Stderr, defaultDeps()))
}

func run(args []string, stderr io.Writer, deps runDeps) int {
	var kubeconfigPath string

	flags := flag.NewFlagSet(deps.name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&kubeconfigPath, "kubeconfig", "", kubeconfigUsage)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}

	if err := checkHostsFilePermission(deps.hostsPath); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return exitFailure
	}

	lock, err := deps.acquire(deps.lockPath)
	if errors.Is(err, instance.ErrAlreadyRunning) {
		fmt.Fprintln(stderr, "Error: another kube-service-tunnel instance is already running")
		return exitFailure
	}
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return exitFailure
	}
	defer lock.Release()

	deps.silenceKubeLogs()
	if err := deps.runTUI(kubeconfigPath); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return exitFailure
	}
	return exitOK
}
