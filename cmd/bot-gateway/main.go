package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/zonprox/Signy/internal/bot"
	"github.com/zonprox/Signy/internal/config"
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

	logger.Info("starting bot-gateway",
		"mode", cfg.TelegramMode,
		"storage", cfg.StoragePath,
		"has_master_key", cfg.HasMasterKey(),
	)

	b, err := bot.New(cfg, logger)
	if err != nil {
		logger.Error("failed to create bot", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("received shutdown signal", "signal", sig)
		cancel()
	}()

	if err := b.Run(ctx); err != nil {
		logger.Error("bot exited with error", "error", err)
		os.Exit(1)
	}

	logger.Info("bot-gateway stopped gracefully")
}
