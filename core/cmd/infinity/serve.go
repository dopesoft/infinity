package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dopesoft/infinity/core/internal/server"
	"github.com/spf13/cobra"
)

func serveCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the Core HTTP/WebSocket server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if v := os.Getenv("PORT"); v != "" {
				addr = ":" + v
			}
			srv := server.New(server.Config{
				Addr:    addr,
				Version: version,
			})

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			errCh := make(chan error, 1)
			go func() { errCh <- srv.Start() }()

			fmt.Printf("infinity core listening on %s\n", addr)

			select {
			case err := <-errCh:
				return err
			case <-ctx.Done():
				fmt.Println("shutdown signal received")
				shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancelShutdown()
				return srv.Shutdown(shutdownCtx)
			}
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":8080", "listen address (or use $PORT)")
	return cmd
}
