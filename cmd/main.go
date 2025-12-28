package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/byoungmin/kube-service-tunnel/cmd/tui"
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
	var kubeconfigPath string

	flag.StringVar(&kubeconfigPath, "kubeconfig", "", "Path to kubeconfig file (default: ~/.kube/config)")
	flag.Parse()

	if err := checkHostsFilePermission(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := tui.Run(kubeconfigPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
