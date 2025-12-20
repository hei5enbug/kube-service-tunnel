package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/byoungmin/kube-service-tunnel/internal/dns"
)

func main() {
	dnsManager := dns.NewDNSManagerInstance()
	_ = dnsManager

	fmt.Println("DNS manager initialized.")
	fmt.Println("Press Ctrl+C to exit...")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	<-sigChan
	fmt.Println("\nShutting down...")
}
