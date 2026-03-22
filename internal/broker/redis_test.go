package broker_test

import (
	"context"
	"testing"
	"time"

	"github.com/OneLastStop529/taskforge/internal/broker"
	"github.com/OneLastStop529/taskforge/internal/task"
)

type scheduledEntry struct {
	runAt time.Time
	value []byte
}

type fakeRedisBrokerClient struct {
	ready      map[string][][]byte
	processing map[string][][]byte
	leases     map[string]map[string]time.Time
	scheduled  map[string][]scheduledEntry
	closed     bool
}

func newFakeRedisBrokerClient() *fakeRedisBrokerClient {
	return &fakeRedisBrokerClient{
		ready:      make(map[string][][]byte),
		processing: make(map[string][][]byte),
		leases:     make(map[string]map[string]time.Time),
		scheduled:  make(map[string][]scheduledEntry),
	}
}

func (c *fakeRedisBrokerClient) EnqueueReady(_ context.Context, key string, value []byte) error {
	c.ready[key] = append(c.ready[key], append([]byte(nil), value...))
	return nil
}

func (c *fakeRedisBrokerClient) ReserveReady(_ context.Context, readyKey, processingKey, leaseKey string, leaseUntil time.Time) ([]byte, error) {
	values := c.ready[readyKey]
	if len(values) == 0 {
		return nil, nil
	}
	value := append([]byte(nil), values[0]...)
	c.ready[readyKey] = values[1:]
	c.processing[processingKey] = append(c.processing[processingKey], append([]byte(nil), value...))
	if c.leases[leaseKey] == nil {
		c.leases[leaseKey] = make(map[string]time.Time)
	}
	c.leases[leaseKey][string(value)] = leaseUntil
	return value, nil
}

func (c *fakeRedisBrokerClient) AckReservation(_ context.Context, processingKey, leaseKey string, value []byte) error {
	values := c.processing[processingKey]
	for i, existing := range values {
		if string(existing) == string(value) {
			c.processing[processingKey] = append(values[:i], values[i+1:]...)
			break
		}
	}
	if c.leases[leaseKey] != nil {
		delete(c.leases[leaseKey], string(value))
	}
	return nil
}

func (c *fakeRedisBrokerClient) RecoverExpired(_ context.Context, readyKey, processingKey, leaseKey string, now time.Time, count int64) error {
	if c.leases[leaseKey] == nil {
		return nil
	}
	recovered := int64(0)
	values := c.processing[processingKey]
	var remaining [][]byte
	for _, value := range values {
		if recovered < count {
			if expiry, ok := c.leases[leaseKey][string(value)]; ok && !expiry.After(now) {
				c.ready[readyKey] = append(c.ready[readyKey], append([]byte(nil), value...))
				delete(c.leases[leaseKey], string(value))
				recovered++
				continue
			}
		}
		remaining = append(remaining, value)
	}
	c.processing[processingKey] = remaining
	return nil
}

func (c *fakeRedisBrokerClient) legacyDequeueReady(_ context.Context, key string) ([]byte, error) {
	values := c.ready[key]
	if len(values) == 0 {
		return nil, nil
	}
	value := append([]byte(nil), values[0]...)
	c.ready[key] = values[1:]
	return value, nil
}

func (c *fakeRedisBrokerClient) EnqueueScheduled(_ context.Context, key string, value []byte, runAt time.Time) error {
	c.scheduled[key] = append(c.scheduled[key], scheduledEntry{
		runAt: runAt,
		value: append([]byte(nil), value...),
	})
	return nil
}

func (c *fakeRedisBrokerClient) PromoteScheduledReady(_ context.Context, delayedKey, readyKey string, now time.Time, count int64) error {
	entries := c.scheduled[delayedKey]
	if len(entries) == 0 {
		return nil
	}
	var remaining []scheduledEntry
	var promoted [][]byte
	for _, entry := range entries {
		if int64(len(promoted)) < count && !entry.runAt.After(now) {
			promoted = append(promoted, append([]byte(nil), entry.value...))
			continue
		}
		remaining = append(remaining, entry)
	}
	c.scheduled[delayedKey] = remaining
	for _, value := range promoted {
		c.ready[readyKey] = append(c.ready[readyKey], value)
	}
	return nil
}

func (c *fakeRedisBrokerClient) Close() error {
	c.closed = true
	return nil
}

func TestRedisBroker_EnqueueDequeue(t *testing.T) {
	b := broker.NewRedisBrokerWithClient(newFakeRedisBrokerClient(), broker.RedisConfig{})
	defer b.Close() //nolint:errcheck

	ctx := context.Background()
	msg := newMsg("1", "ping", "default")
	if err := b.Enqueue(ctx, msg); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	got, err := b.Dequeue(ctx, []string{"default"})
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if got.ID != msg.ID {
		t.Errorf("got ID %q, want %q", got.ID, msg.ID)
	}
}

func TestRedisBroker_MultipleQueues(t *testing.T) {
	b := broker.NewRedisBrokerWithClient(newFakeRedisBrokerClient(), broker.RedisConfig{})
	defer b.Close() //nolint:errcheck

	ctx := context.Background()
	_ = b.Enqueue(ctx, newMsg("a", "t1", "q1"))
	_ = b.Enqueue(ctx, newMsg("b", "t2", "q2"))

	got, err := b.Dequeue(ctx, []string{"q2", "q1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Queue != "q2" {
		t.Errorf("expected q2, got %q", got.Queue)
	}
}

func TestRedisBroker_Ack(t *testing.T) {
	client := newFakeRedisBrokerClient()
	b := broker.NewRedisBrokerWithClient(client, broker.RedisConfig{})
	ctx := context.Background()
	msg := newMsg("x", "t", "default")
	_ = b.Enqueue(ctx, msg)
	got, _ := b.Dequeue(ctx, []string{"default"})
	if err := b.Ack(ctx, got); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if len(client.processing["taskforge:queue:processing:default"]) != 0 {
		t.Fatal("expected processing queue to be empty after ack")
	}
	if len(client.leases["taskforge:queue:lease:default"]) != 0 {
		t.Fatal("expected lease set to be empty after ack")
	}
}

func TestRedisBroker_Nack_Requeues(t *testing.T) {
	b := broker.NewRedisBrokerWithClient(newFakeRedisBrokerClient(), broker.RedisConfig{})
	ctx := context.Background()
	msg := newMsg("y", "t", "default")
	_ = b.Enqueue(ctx, msg)
	got, _ := b.Dequeue(ctx, []string{"default"})
	if err := b.Nack(ctx, got); err != nil {
		t.Fatalf("Nack: %v", err)
	}
	got2, err := b.Dequeue(ctx, []string{"default"})
	if err != nil {
		t.Fatal(err)
	}
	if got2.ID != msg.ID {
		t.Errorf("expected re-queued message %q, got %q", msg.ID, got2.ID)
	}
}

func TestRedisBroker_ContextCancelled(t *testing.T) {
	b := broker.NewRedisBrokerWithClient(newFakeRedisBrokerClient(), broker.RedisConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := b.Dequeue(ctx, []string{"empty"})
	if err == nil {
		t.Error("expected error when context is already cancelled")
	}
}

func TestRedisBroker_ScheduledDelay(t *testing.T) {
	b := broker.NewRedisBrokerWithClient(newFakeRedisBrokerClient(), broker.RedisConfig{})
	ctx := context.Background()
	msg := newMsg("z", "delayed", "default")
	msg.ScheduledAt = time.Now().Add(50 * time.Millisecond)
	_ = b.Enqueue(ctx, msg)

	dctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	_, err := b.Dequeue(dctx, []string{"default"})
	if err == nil {
		t.Error("expected timeout; message should not be available yet")
	}

	time.Sleep(80 * time.Millisecond)
	dctx2, cancel2 := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel2()
	got, err := b.Dequeue(dctx2, []string{"default"})
	if err != nil {
		t.Fatalf("expected delayed message to be available: %v", err)
	}
	if got.ID != msg.ID {
		t.Errorf("got %q, want %q", got.ID, msg.ID)
	}
}

func TestNewRedisBroker_RequiresAddr(t *testing.T) {
	_, err := broker.NewRedisBroker(broker.RedisConfig{})
	if err == nil {
		t.Fatal("expected constructor error")
	}
}

func TestNewRedisBroker_ConstructsClient(t *testing.T) {
	b, err := broker.NewRedisBroker(broker.RedisConfig{Addr: "127.0.0.1:6379"})
	if err != nil {
		t.Fatalf("NewRedisBroker: %v", err)
	}
	defer b.Close() //nolint:errcheck
}

func TestRedisBroker_Close(t *testing.T) {
	client := newFakeRedisBrokerClient()
	b := broker.NewRedisBrokerWithClient(client, broker.RedisConfig{})
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !client.closed {
		t.Fatal("expected redis client to be closed")
	}
}

func TestRedisBroker_RecoversExpiredReservations(t *testing.T) {
	client := newFakeRedisBrokerClient()
	b := broker.NewRedisBrokerWithClient(client, broker.RedisConfig{LeaseTimeout: 20 * time.Millisecond})
	ctx := context.Background()
	msg := newMsg("lease", "t", "default")
	if err := b.Enqueue(ctx, msg); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	got, err := b.Dequeue(ctx, []string{"default"})
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if got.ID != msg.ID {
		t.Fatalf("got ID %q, want %q", got.ID, msg.ID)
	}

	time.Sleep(30 * time.Millisecond)
	got2, err := b.Dequeue(ctx, []string{"default"})
	if err != nil {
		t.Fatalf("Dequeue after lease expiry: %v", err)
	}
	if got2.ID != msg.ID {
		t.Fatalf("got ID %q after recovery, want %q", got2.ID, msg.ID)
	}
}

var _ broker.Broker = (*broker.RedisBroker)(nil)
var _ = task.StatePending
