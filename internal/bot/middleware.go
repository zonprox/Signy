package bot

import (
	"log/slog"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/zonprox/Signy/internal/metrics"
)

// callbackDebouncer prevents duplicate callback processing.
type callbackDebouncer struct {
	mu       sync.Mutex
	lastSeen map[string]time.Time
	interval time.Duration
}

func newCallbackDebouncer(interval time.Duration) *callbackDebouncer {
	return &callbackDebouncer{
		lastSeen: make(map[string]time.Time),
		interval: interval,
	}
}

// ShouldProcess returns true if this callback should be processed (not a duplicate).
func (d *callbackDebouncer) ShouldProcess(callbackID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	if last, ok := d.lastSeen[callbackID]; ok {
		if now.Sub(last) < d.interval {
			return false
		}
	}
	d.lastSeen[callbackID] = now

	// Periodic cleanup of old entries
	if len(d.lastSeen) > 10000 {
		cutoff := now.Add(-d.interval * 10)
		for k, v := range d.lastSeen {
			if v.Before(cutoff) {
				delete(d.lastSeen, k)
			}
		}
	}

	return true
}

// rateLimiter implements per-user rate limiting.
type rateLimiter struct {
	mu       sync.Mutex
	requests map[int64][]time.Time
	limit    int
	window   time.Duration
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		requests: make(map[int64][]time.Time),
		limit:    limit,
		window:   window,
	}
}

// Allow returns true if the user is within rate limits.
func (r *rateLimiter) Allow(userID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-r.window)

	// Clean old requests
	reqs := r.requests[userID]
	valid := reqs[:0]
	for _, t := range reqs {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= r.limit {
		return false
	}

	r.requests[userID] = append(valid, now)
	return true
}

// logMiddleware wraps update processing with logging and metrics.
func logMiddleware(logger *slog.Logger, update tgbotapi.Update) {
	if update.Message != nil {
		metrics.TelegramUpdatesTotal.WithLabelValues("message").Inc()
		logger.Info("update received",
			"type", "message",
			"user_id", update.Message.From.ID,
			"text_len", len(update.Message.Text),
		)
	} else if update.CallbackQuery != nil {
		metrics.TelegramUpdatesTotal.WithLabelValues("callback").Inc()
		logger.Info("update received",
			"type", "callback",
			"user_id", update.CallbackQuery.From.ID,
			"data", update.CallbackQuery.Data,
		)
	}
}
