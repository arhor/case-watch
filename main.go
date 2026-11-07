package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/arhor/case-watch/internal/application"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := application.Run(ctx)

	stop()

	if err != nil {
		log.Fatal(err)
	}
}
