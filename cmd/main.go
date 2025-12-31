package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/byoungmin/kube-service-tunnel/cmd/tui"
	"github.com/byoungmin/kube-service-tunnel/internal/instance"
)

func checkHostsFilePermission() error {
	const hostsPath = "/etc/hosts"
	file, err := os.OpenFile(hostsPath, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w\n\nPlease run this program with sudo or ensure you have write permission to /etc/hosts", hostsPath, err)
	}
	file.Close()
	return nil
}

func main() {
	os.Exit(run())
}

func run() int {
	var kubeconfigPath string

	flag.StringVar(&kubeconfigPath, "kubeconfig", "", "Path to kubeconfig file (default: ~/.kube/config)")
	flag.Parse()

	if err := checkHostsFilePermission(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	lock, err := instance.Acquire(instance.DefaultLockPath)
	if errors.Is(err, instance.ErrAlreadyRunning) {
		fmt.Fprintln(os.Stderr, "Error: another kube-service-tunnel instance is already running")
		return 1
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	defer lock.Release()

	if err := tui.Run(kubeconfigPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}
