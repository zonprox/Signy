package bot

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/redis/go-redis/v9"
	"github.com/zonprox/Signy/internal/bot/fsm"
	"github.com/zonprox/Signy/internal/certset"
	"github.com/zonprox/Signy/internal/config"
	"github.com/zonprox/Signy/internal/health"
	"github.com/zonprox/Signy/internal/job"
	"github.com/zonprox/Signy/internal/queue"
	"github.com/zonprox/Signy/internal/storage"
)

// Bot is the main bot-gateway service orchestrator.
type Bot struct {
	api      *tgbotapi.BotAPI
	cfg      *config.Config
	rdb      *redis.Client
	handlers *Handlers
	health   *health.Server
	logger   *slog.Logger
}

// New creates a new Bot instance.
func New(cfg *config.Config, logger *slog.Logger) (*Bot, error) {
	// Initialize Telegram bot API
	api, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		return nil, fmt.Errorf("telegram bot api: %w", err)
	}
	logger.Info("telegram bot authorized", "username", api.Self.UserName)

	// Initialize Redis
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	rdb := redis.NewClient(opts)

	// Verify Redis connectivity
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	// Initialize components
	store := storage.NewManager(cfg.StoragePath, logger)
	certMgr := certset.NewManager(store, logger, cfg.MasterKey, cfg.MaxCertSetsPerUser)
	producer := queue.NewProducer(rdb, logger)
	jobMgr := job.NewManager(rdb, store, producer, logger)
	fsmStore := fsm.NewStore()
	healthSrv := health.NewServer(rdb, logger)

	handlers := NewHandlers(api, cfg, store, certMgr, jobMgr, fsmStore, rdb, logger)

	return &Bot{
		api:      api,
		cfg:      cfg,
		rdb:      rdb,
		handlers: handlers,
		health:   healthSrv,
		logger:   logger,
	}, nil
}

// Run starts the bot and blocks until context is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	// Start health server
	healthAddr := ":8080"
	httpSrv := &http.Server{Addr: healthAddr, Handler: b.health.Handler()}
	go func() {
		b.logger.Info("health server starting", "addr", healthAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			b.logger.Error("health server error", "error", err)
		}
	}()

	// Start job event subscriber for push notifications
	go b.handlers.SubscribeJobEvents(ctx)

	// Start Telegram update processing
	var err error
	if b.cfg.TelegramMode == "webhook" {
		err = b.runWebhook(ctx)
	} else {
		err = b.runPolling(ctx)
	}

	// Graceful shutdown
	b.logger.Info("shutting down health server")
	_ = httpSrv.Shutdown(context.Background())
	b.rdb.Close()

	return err
}

func (b *Bot) runPolling(ctx context.Context) error {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := b.api.GetUpdatesChan(u)

	b.logger.Info("bot started in polling mode")

	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			b.logger.Info("polling stopped")
			return nil
		case update := <-updates:
			go b.handlers.HandleUpdate(ctx, update)
		}
	}
}

func (b *Bot) runWebhook(ctx context.Context) error {
	wh, err := tgbotapi.NewWebhook(b.cfg.TelegramWebhookURL)
	if err != nil {
		return fmt.Errorf("set webhook: %w", err)
	}
	_, err = b.api.Request(wh)
	if err != nil {
		return fmt.Errorf("register webhook: %w", err)
	}

	b.logger.Info("bot started in webhook mode", "url", b.cfg.TelegramWebhookURL)

	updates := b.api.ListenForWebhook("/webhook")

	go func() {
		_ = http.ListenAndServe(":8443", nil) //nolint:errcheck // webhook server
	}()

	for {
		select {
		case <-ctx.Done():
			b.logger.Info("webhook stopped")
			return nil
		case update := <-updates:
			go b.handlers.HandleUpdate(ctx, update)
		}
	}
}
