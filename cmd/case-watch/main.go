package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/arhor/case-watch/internal/app"
)

func main() {
	ctx, stop :=
		signal.NotifyContext(
			context.Background(),
			os.Interrupt,
			syscall.SIGTERM,
		)

	defer stop()

	if err := app.Run(ctx, os.LookupEnv, app.Factories{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
