package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zonprox/Signy/internal/config"
	"github.com/zonprox/Signy/internal/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	logger.Info("starting signing-worker",
		"concurrency", cfg.WorkerConcurrency,
		"visibility_timeout", cfg.VisibilityTimeoutSeconds,
		"storage", cfg.StoragePath,
		"has_master_key", cfg.HasMasterKey(),
		"zsign_mock", os.Getenv("ZSIGN_MOCK"),
	)

	w, err := worker.New(cfg, logger)
	if err != nil {
		logger.Error("failed to create worker", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown with timeout
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("received shutdown signal, finishing in-flight jobs", "signal", sig)
		cancel()

		// Force exit after 90 seconds
		time.AfterFunc(90*time.Second, func() {
			logger.Error("graceful shutdown timeout, forcing exit")
			os.Exit(1)
		})
	}()

	if err := w.Run(ctx); err != nil {
		logger.Error("worker exited with error", "error", err)
		os.Exit(1)
	}

	logger.Info("signing-worker stopped gracefully")
}
