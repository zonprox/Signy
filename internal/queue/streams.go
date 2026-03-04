package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zonprox/Signy/internal/metrics"
)

const (
	// StreamName is the Redis stream key for signing jobs.
	StreamName = "signy:jobs:stream"
	// GroupName is the consumer group name.
	GroupName = "signing-workers"
	// DefaultBlockDuration is how long XREADGROUP blocks.
	DefaultBlockDuration = 5 * time.Second
)

// Message represents a dequeued job message.
type Message struct {
	ID      string
	Payload map[string]interface{}
}

// Producer enqueues jobs into the Redis stream.
type Producer struct {
	rdb    *redis.Client
	logger *slog.Logger
}

// NewProducer creates a new queue producer.
func NewProducer(rdb *redis.Client, logger *slog.Logger) *Producer {
	return &Producer{rdb: rdb, logger: logger}
}

// Enqueue adds a job to the stream. Returns the message ID.
func (p *Producer) Enqueue(ctx context.Context, jobData map[string]string) (string, error) {
	id, err := p.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamName,
		Values: jobData,
	}).Result()
	if err != nil {
		metrics.RedisErrors.WithLabelValues("xadd").Inc()
		return "", fmt.Errorf("xadd: %w", err)
	}
	p.logger.Info("job enqueued", "stream_id", id, "job_id", jobData["job_id"])
	return id, nil
}

// Consumer reads jobs from the Redis stream with reliable delivery.
type Consumer struct {
	rdb              *redis.Client
	logger           *slog.Logger
	consumerName     string
	visibilityTimeout time.Duration
}

// NewConsumer creates a new queue consumer.
func NewConsumer(rdb *redis.Client, logger *slog.Logger, consumerName string, visibilityTimeout time.Duration) *Consumer {
	return &Consumer{
		rdb:              rdb,
		logger:           logger,
		consumerName:     consumerName,
		visibilityTimeout: visibilityTimeout,
	}
}

// EnsureGroup creates the consumer group if it doesn't exist.
func (c *Consumer) EnsureGroup(ctx context.Context) error {
	err := c.rdb.XGroupCreateMkStream(ctx, StreamName, GroupName, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return fmt.Errorf("create consumer group: %w", err)
	}
	return nil
}

// ReadNew reads new messages from the stream. Blocks for up to blockDuration.
func (c *Consumer) ReadNew(ctx context.Context, count int64) ([]Message, error) {
	streams, err := c.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    GroupName,
		Consumer: c.consumerName,
		Streams:  []string{StreamName, ">"},
		Count:    count,
		Block:    DefaultBlockDuration,
	}).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		metrics.RedisErrors.WithLabelValues("xreadgroup").Inc()
		return nil, fmt.Errorf("xreadgroup: %w", err)
	}

	return convertStreams(streams), nil
}

// ClaimStale claims messages that have been idle beyond the visibility timeout.
// This handles crash recovery — if a worker died while processing a message.
func (c *Consumer) ClaimStale(ctx context.Context, count int64) ([]Message, error) {
	msgs, _, err := c.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   StreamName,
		Group:    GroupName,
		Consumer: c.consumerName,
		MinIdle:  c.visibilityTimeout,
		Start:    "0-0",
		Count:    count,
	}).Result()
	if err != nil {
		metrics.RedisErrors.WithLabelValues("xautoclaim").Inc()
		return nil, fmt.Errorf("xautoclaim: %w", err)
	}

	var result []Message
	for _, msg := range msgs {
		result = append(result, Message{
			ID:      msg.ID,
			Payload: msg.Values,
		})
	}
	return result, nil
}

// Ack acknowledges a message as successfully processed.
func (c *Consumer) Ack(ctx context.Context, messageID string) error {
	err := c.rdb.XAck(ctx, StreamName, GroupName, messageID).Err()
	if err != nil {
		metrics.RedisErrors.WithLabelValues("xack").Inc()
		return fmt.Errorf("xack: %w", err)
	}
	return nil
}

// PendingCount returns the number of pending messages in the group.
func (c *Consumer) PendingCount(ctx context.Context) (int64, error) {
	info, err := c.rdb.XPending(ctx, StreamName, GroupName).Result()
	if err != nil {
		return 0, fmt.Errorf("xpending: %w", err)
	}
	return info.Count, nil
}

// StreamLen returns the total length of the stream.
func (c *Consumer) StreamLen(ctx context.Context) (int64, error) {
	return c.rdb.XLen(ctx, StreamName).Result()
}

func convertStreams(streams []redis.XStream) []Message {
	var msgs []Message
	for _, stream := range streams {
		for _, xmsg := range stream.Messages {
			msgs = append(msgs, Message{
				ID:      xmsg.ID,
				Payload: xmsg.Values,
			})
		}
	}
	return msgs
}

// EncodeJobPayload serializes a job ID and metadata into stream values.
func EncodeJobPayload(jobID string, userID int64, certSetID, ipaPath string) map[string]string {
	return map[string]string{
		"job_id":     jobID,
		"user_id":    fmt.Sprintf("%d", userID),
		"certset_id": certSetID,
		"ipa_path":   ipaPath,
	}
}

// DecodeJobPayload extracts job fields from stream values.
func DecodeJobPayload(payload map[string]interface{}) (jobID string, userID string, certSetID string, ipaPath string, err error) {
	var ok bool
	jobID, ok = payload["job_id"].(string)
	if !ok {
		return "", "", "", "", fmt.Errorf("missing or invalid job_id")
	}
	userID, ok = payload["user_id"].(string)
	if !ok {
		return "", "", "", "", fmt.Errorf("missing or invalid user_id")
	}
	certSetID, ok = payload["certset_id"].(string)
	if !ok {
		return "", "", "", "", fmt.Errorf("missing or invalid certset_id")
	}
	ipaPath, ok = payload["ipa_path"].(string)
	if !ok {
		return "", "", "", "", fmt.Errorf("missing or invalid ipa_path")
	}
	return
}

// PublishJobEvent publishes a job status event via Redis Pub/Sub for the bot.
func PublishJobEvent(ctx context.Context, rdb *redis.Client, jobID string, status string, message string) error {
	event := map[string]string{
		"job_id":  jobID,
		"status":  status,
		"message": message,
		"time":    time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return rdb.Publish(ctx, "signy:job:events", string(data)).Err()
}
