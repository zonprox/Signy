package queue

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func getTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		url = "redis://localhost:6379/15" // use DB 15 for tests
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	rdb := redis.NewClient(opts)
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("redis not available: %v", err)
	}
	// Clean test keys
	rdb.Del(ctx, StreamName)
	return rdb
}

func TestEnqueueAndReadNew(t *testing.T) {
	rdb := getTestRedis(t)
	defer rdb.Close()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	producer := NewProducer(rdb, logger)
	consumer := NewConsumer(rdb, logger, "test-worker-1", 10*time.Second)

	if err := consumer.EnsureGroup(ctx); err != nil {
		t.Fatalf("EnsureGroup: %v", err)
	}

	// Enqueue a job
	payload := EncodeJobPayload("job-1", 12345, "certset-1", "/storage/test.ipa")
	msgID, err := producer.Enqueue(ctx, payload)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if msgID == "" {
		t.Fatal("expected non-empty message ID")
	}

	// Read it
	msgs, err := consumer.ReadNew(ctx, 10)
	if err != nil {
		t.Fatalf("ReadNew: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	jobID, userID, certSetID, ipaPath, err := DecodeJobPayload(msgs[0].Payload)
	if err != nil {
		t.Fatalf("DecodeJobPayload: %v", err)
	}
	if jobID != "job-1" || userID != "12345" || certSetID != "certset-1" || ipaPath != "/storage/test.ipa" {
		t.Fatalf("unexpected payload: job=%s user=%s cert=%s ipa=%s", jobID, userID, certSetID, ipaPath)
	}
}

func TestAck(t *testing.T) {
	rdb := getTestRedis(t)
	defer rdb.Close()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// Clean and recreate
	rdb.Del(ctx, StreamName)
	producer := NewProducer(rdb, logger)
	consumer := NewConsumer(rdb, logger, "test-worker-ack", 10*time.Second)
	consumer.EnsureGroup(ctx)

	payload := EncodeJobPayload("job-ack-1", 100, "cs-1", "/test.ipa")
	producer.Enqueue(ctx, payload)

	msgs, _ := consumer.ReadNew(ctx, 10)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(msgs))
	}

	// ACK the message
	if err := consumer.Ack(ctx, msgs[0].ID); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	// Pending count should be 0
	pending, err := consumer.PendingCount(ctx)
	if err != nil {
		t.Fatalf("PendingCount: %v", err)
	}
	if pending != 0 {
		t.Fatalf("expected 0 pending, got %d", pending)
	}
}

func TestClaimStale(t *testing.T) {
	rdb := getTestRedis(t)
	defer rdb.Close()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	rdb.Del(ctx, StreamName)
	producer := NewProducer(rdb, logger)

	// Consumer 1 reads but "crashes" (never ACKs)
	consumer1 := NewConsumer(rdb, logger, "crashed-worker", 100*time.Millisecond) // very short timeout for test
	consumer1.EnsureGroup(ctx)

	payload := EncodeJobPayload("job-stale-1", 200, "cs-2", "/stale.ipa")
	producer.Enqueue(ctx, payload)

	msgs, _ := consumer1.ReadNew(ctx, 10)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(msgs))
	}

	// Wait for message to become stale
	time.Sleep(200 * time.Millisecond)

	// Consumer 2 claims stale messages
	consumer2 := NewConsumer(rdb, logger, "recovery-worker", 100*time.Millisecond)
	claimed, err := consumer2.ClaimStale(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimStale: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("expected 1 claimed message, got %d", len(claimed))
	}

	jobID, _, _, _, err := DecodeJobPayload(claimed[0].Payload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if jobID != "job-stale-1" {
		t.Fatalf("expected job-stale-1, got %s", jobID)
	}

	// ACK the claimed message
	if err := consumer2.Ack(ctx, claimed[0].ID); err != nil {
		t.Fatalf("Ack claimed: %v", err)
	}
}

func TestMultipleEnqueueAndRead(t *testing.T) {
	rdb := getTestRedis(t)
	defer rdb.Close()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	rdb.Del(ctx, StreamName)
	producer := NewProducer(rdb, logger)
	consumer := NewConsumer(rdb, logger, "multi-worker", 10*time.Second)
	consumer.EnsureGroup(ctx)

	for i := 0; i < 5; i++ {
		payload := EncodeJobPayload(fmt.Sprintf("job-multi-%d", i), int64(i), "cs", "/test.ipa")
		producer.Enqueue(ctx, payload)
	}

	msgs, err := consumer.ReadNew(ctx, 10)
	if err != nil {
		t.Fatalf("ReadNew: %v", err)
	}
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(msgs))
	}

	for _, msg := range msgs {
		consumer.Ack(ctx, msg.ID)
	}

	pending, _ := consumer.PendingCount(ctx)
	if pending != 0 {
		t.Fatalf("expected 0 pending after acking all, got %d", pending)
	}
}

func TestDecodeJobPayloadErrors(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]interface{}
	}{
		{"missing job_id", map[string]interface{}{"user_id": "1", "certset_id": "c", "ipa_path": "p"}},
		{"missing user_id", map[string]interface{}{"job_id": "j", "certset_id": "c", "ipa_path": "p"}},
		{"missing certset_id", map[string]interface{}{"job_id": "j", "user_id": "1", "ipa_path": "p"}},
		{"missing ipa_path", map[string]interface{}{"job_id": "j", "user_id": "1", "certset_id": "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, _, err := DecodeJobPayload(tt.payload)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
