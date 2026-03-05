package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/zonprox/Signy/internal/bot"
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

	logger.Info("starting signy",
		"storage", cfg.StoragePath,
		"has_master_key", cfg.HasMasterKey(),
		"zsign_mock", os.Getenv("ZSIGN_MOCK"),
		"worker_concurrency", cfg.WorkerConcurrency,
	)

	// Create bot-gateway
	b, err := bot.New(cfg, logger.With("component", "bot-gateway"))
	if err != nil {
		logger.Error("failed to create bot", "error", err)
		os.Exit(1)
	}

	// Create signing-worker
	w, err := worker.New(cfg, logger.With("component", "signing-worker"))
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
		logger.Info("received shutdown signal", "signal", sig)
		cancel()

		// Force exit after 90 seconds
		time.AfterFunc(90*time.Second, func() {
			logger.Error("graceful shutdown timeout, forcing exit")
			os.Exit(1)
		})
	}()

	// Run both services concurrently
	var wg sync.WaitGroup
	errs := make(chan error, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := b.Run(ctx); err != nil {
			errs <- err
		}
	}()
	go func() {
		defer wg.Done()
		if err := w.Run(ctx); err != nil {
			errs <- err
		}
	}()

	// Wait for both to finish
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			logger.Error("service exited with error", "error", err)
			os.Exit(1)
		}
	}

	logger.Info("signy stopped gracefully")
}
