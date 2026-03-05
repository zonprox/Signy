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
	"github.com/zonprox/Signy/internal/crypto"
	"github.com/zonprox/Signy/internal/job"
	"github.com/zonprox/Signy/internal/queue"
	"github.com/zonprox/Signy/internal/storage"
	"github.com/zonprox/Signy/internal/web"
)

// Bot is the main bot-gateway service orchestrator.
type Bot struct {
	api      *tgbotapi.BotAPI
	cfg      *config.Config
	rdb      *redis.Client
	handlers *Handlers
	web      *web.Server
	logger   *slog.Logger
}

// New creates a new Bot instance.
func New(cfg *config.Config, logger *slog.Logger) (*Bot, error) {
	// Connect to local Telegram API always
	api, err := tgbotapi.NewBotAPIWithAPIEndpoint(cfg.TelegramBotToken, "http://telegram-bot-api:8081/bot%s/%s")
	if err != nil {
		return nil, fmt.Errorf("telegram bot api: %w", err)
	}
	logger.Info("telegram bot authorized", "username", api.Self.UserName)

	// Register bot commands
	commands := tgbotapi.NewSetMyCommands(
		tgbotapi.BotCommand{Command: "start", Description: "🏠 Main menu"},
		tgbotapi.BotCommand{Command: "sign", Description: "➕ New signing job"},
		tgbotapi.BotCommand{Command: "certs", Description: "🪪 Certificate management"},
		tgbotapi.BotCommand{Command: "jobs", Description: "🧾 My jobs"},
		tgbotapi.BotCommand{Command: "help", Description: "❓ Help"},
	)
	if _, err := api.Request(commands); err != nil {
		logger.Warn("failed to set bot commands", "error", err)
	}

	// Initialize Redis
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	rdb := redis.NewClient(opts)
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	// Initialize components
	store := storage.NewManager(cfg.StoragePath, logger)
	certMgr := certset.NewManager(store, logger, cfg.MasterKey, cfg.MaxCertSetsPerUser)
	producer := queue.NewProducer(rdb, logger)
	jobMgr := job.NewManager(rdb, store, producer, logger)
	fsmStore := fsm.NewStore()

	web, err := web.NewServer(cfg, jobMgr, certMgr, rdb, logger)
	if err != nil {
		return nil, fmt.Errorf("create web server: %w", err)
	}

	// Process key for ephemeral password encryption (shared with Worker via MASTER_KEY)
	var processKey []byte
	if cfg.HasMasterKey() {
		processKey, err = crypto.DeriveSharedEphemeralKey(cfg.MasterKey)
		if err != nil {
			return nil, fmt.Errorf("derive shared ephemeral key: %w", err)
		}
	} else {
		processKey, err = crypto.GenerateRandomKey()
		if err != nil {
			return nil, fmt.Errorf("generate random key: %w", err)
		}
	}

	handlers := NewHandlers(api, cfg, store, certMgr, jobMgr, fsmStore, rdb, logger, processKey)

	return &Bot{
		api:      api,
		cfg:      cfg,
		rdb:      rdb,
		handlers: handlers,
		web:      web,
		logger:   logger,
	}, nil
}

// Run starts the bot in polling mode and blocks until context is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	// Start web server (health + UI) on configured port
	addr := ":" + b.cfg.AppPort
	httpSrv := &http.Server{Addr: addr, Handler: b.web.Handler()}
	go func() {
		b.logger.Info("web server starting", "addr", addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			b.logger.Error("web server error", "error", err)
		}
	}()

	// Push notifications subscription
	go b.handlers.SubscribeJobEvents(ctx)

	// Long-polling loop
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := b.api.GetUpdatesChan(u)
	b.logger.Info("bot started (polling mode)")

	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			_ = httpSrv.Shutdown(context.Background())
			_ = b.rdb.Close()
			b.logger.Info("bot stopped")
			return nil
		case update := <-updates:
			go b.handlers.HandleUpdate(ctx, update)
		}
	}
}
