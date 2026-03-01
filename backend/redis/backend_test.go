package redis

import (
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/your_github_user_or_org/zapalert/internal/level"
)

func TestRedisBackendKeyingAndSnapshot(t *testing.T) {
	mini := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: mini.Addr()})
	defer client.Close()

	b, err := New(Config{
		Client:      client,
		Service:     "svc-a",
		Prefix:      "zapalert",
		Window:      time.Minute,
		BucketCount: 6,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	base := time.Unix(1_700_000_400, 0).UTC()
	method := "orders.create"

	if err := b.IncrRequest(method, false, base); err != nil {
		t.Fatalf("IncrRequest() error = %v", err)
	}
	if err := b.IncrRequest(method, true, base); err != nil {
		t.Fatalf("IncrRequest() error = %v", err)
	}
	if err := b.IncrAlert(method, level.AlertLevel("P3"), base); err != nil {
		t.Fatalf("IncrAlert() error = %v", err)
	}

	nextBucketTime := base.Add(11 * time.Second)
	if err := b.IncrAlert(method, level.AlertLevel("P3"), nextBucketTime); err != nil {
		t.Fatalf("IncrAlert() second bucket error = %v", err)
	}

	metrics, err := b.Snapshot(method, nextBucketTime)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if metrics.AlertCount != 2 || metrics.RequestTotal != 2 || metrics.RequestFailures != 1 {
		t.Fatalf("unexpected metrics: %+v", metrics)
	}

	bucket := b.bucketAt(base)
	reqKey := b.reqKey(method, bucket)
	alertKey := b.alertKey(method, bucket)

	if !mini.Exists(reqKey) {
		t.Fatalf("expected request key %q to exist", reqKey)
	}
	if !mini.Exists(alertKey) {
		t.Fatalf("expected alert key %q to exist", alertKey)
	}
	if got := mini.HGet(reqKey, "total"); got != "2" {
		t.Fatalf("total = %q, want 2", got)
	}
	if got := mini.HGet(reqKey, "fail"); got != "1" {
		t.Fatalf("fail = %q, want 1", got)
	}
	if ttl := mini.TTL(reqKey); ttl <= time.Minute {
		t.Fatalf("request key ttl = %v, want > 1m", ttl)
	}
}
