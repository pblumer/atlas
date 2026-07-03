// Command atlasd runs the Atlas server: the public API, the operations console,
// and the MCP endpoint (ADR-0011). Every optional surface is on by default;
// disable one with its ATLAS_* environment variable (ADR-0012), e.g.:
//
//	atlasd                 # everything on, listening on 127.0.0.1:8080
//	ATLAS_WEB=off atlasd   # no web console
//	ATLAS_MCP=off atlasd   # no MCP endpoint
//	ATLAS_ADDR=:9000 atlasd
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/pblumer/atlas/server"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg, err := server.ConfigFromEnv()
	if err != nil {
		log.Error("invalid configuration", "err", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := server.New(cfg, log).Run(ctx); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}
