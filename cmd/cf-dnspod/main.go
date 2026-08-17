package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/oustn/cloudflare-dns-dnspod/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(app.DefaultRunner().Run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
