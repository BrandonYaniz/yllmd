package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/BrandonYaniz/yllmd/internal/config"
	"github.com/BrandonYaniz/yllmd/internal/daemon"
	"github.com/BrandonYaniz/yllmd/internal/providers"
	"github.com/BrandonYaniz/yllmd/internal/providers/local"
)

func main() {
	configPath := flag.String("config", "config.example.yaml", "path to YAML configuration")
	useFakeProvider := flag.Bool("fake-provider", false, "use deterministic fake local provider instead of yllama-runner")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var provider providers.Provider
	if *useFakeProvider {
		provider = local.NewFakeProvider(cfg.ModelLifecycle.ResidentModel)
	} else {
		provider = local.NewRunnerProvider(cfg, logger)
	}
	server := daemon.NewServer(cfg, provider, logger)
	logger.Info("starting yllmd", "socket", cfg.Server.SocketPath)
	if err := server.ListenAndServe(ctx); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
