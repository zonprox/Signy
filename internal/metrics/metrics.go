package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// JobsTotal counts total jobs by status.
	JobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signy",
		Name:      "jobs_total",
		Help:      "Total number of signing jobs by final status.",
	}, []string{"status"})

	// JobDurationSeconds observes job signing duration.
	JobDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "signy",
		Name:      "job_duration_seconds",
		Help:      "Duration of signing jobs in seconds.",
		Buckets:   prometheus.ExponentialBuckets(1, 2, 12),
	}, []string{"status"})

	// JobsInFlight tracks currently processing jobs.
	JobsInFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "signy",
		Name:      "jobs_in_flight",
		Help:      "Number of jobs currently being processed.",
	})

	// QueueDepth tracks estimated queue depth.
	QueueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "signy",
		Name:      "queue_depth",
		Help:      "Estimated number of pending jobs in queue.",
	})

	// TelegramUpdatesTotal counts incoming Telegram updates.
	TelegramUpdatesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signy",
		Name:      "telegram_updates_total",
		Help:      "Total Telegram updates received by type.",
	}, []string{"type"})

	// FileDownloadDuration observes file download duration.
	FileDownloadDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "signy",
		Name:      "file_download_duration_seconds",
		Help:      "Duration of Telegram file downloads.",
		Buckets:   prometheus.ExponentialBuckets(0.1, 2, 10),
	})

	// CertSetsTotal tracks cert set operations.
	CertSetsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signy",
		Name:      "certset_operations_total",
		Help:      "Total cert set operations by type.",
	}, []string{"operation"})

	// CleanupFilesRemoved counts cleaned up files.
	CleanupFilesRemoved = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "signy",
		Name:      "cleanup_files_removed_total",
		Help:      "Total files removed during cleanup.",
	})

	// RedisErrors counts Redis operation errors.
	RedisErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "signy",
		Name:      "redis_errors_total",
		Help:      "Total Redis operation errors.",
	}, []string{"operation"})
)
