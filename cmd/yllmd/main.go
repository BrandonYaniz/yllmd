package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/BrandonYaniz/yllmd/internal/config"
	"github.com/BrandonYaniz/yllmd/internal/daemon"
	"github.com/BrandonYaniz/yllmd/internal/locations"
	"github.com/BrandonYaniz/yllmd/internal/providers"
	"github.com/BrandonYaniz/yllmd/internal/providers/local"
)

var version = "dev"

func main() {
	mode := flag.String("mode", string(locations.ModeUser), "operating mode: user or daemon")
	configPath := flag.String("config", "", "path to YAML configuration (overrides mode default)")
	useFakeProvider := flag.Bool("fake-provider", false, "use deterministic fake local provider instead of yllama-runner")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	if *configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "resolve home directory:", err)
			os.Exit(1)
		}
		paths, err := locations.Resolve(locations.Mode(*mode), runtime.GOOS, runtime.GOARCH, home)
		if err != nil {
			fmt.Fprintln(os.Stderr, "resolve mode paths:", err)
			os.Exit(1)
		}
		*configPath = paths.ConfigFile
	}

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
