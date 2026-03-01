package inmem

import (
	"testing"
	"time"

	"github.com/your_github_user_or_org/zapalert/internal/level"
)

func TestBackendWindowRotation(t *testing.T) {
	b, err := New(Config{
		Window:      time.Minute,
		BucketCount: 6,
		MethodTTL:   2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	base := time.Unix(1_700_000_000, 0).UTC()
	method := "payments.charge"

	if err := b.IncrAlert(method, level.AlertLevel("P3"), base); err != nil {
		t.Fatalf("IncrAlert() error = %v", err)
	}
	if err := b.IncrRequest(method, false, base); err != nil {
		t.Fatalf("IncrRequest() error = %v", err)
	}

	metrics, err := b.Snapshot(method, base)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if metrics.AlertCount != 1 || metrics.RequestTotal != 1 || metrics.RequestFailures != 1 {
		t.Fatalf("unexpected initial metrics: %+v", metrics)
	}

	t2 := base.Add(11 * time.Second)
	if err := b.IncrAlert(method, level.AlertLevel("P3"), t2); err != nil {
		t.Fatalf("IncrAlert() error = %v", err)
	}
	if err := b.IncrRequest(method, true, t2); err != nil {
		t.Fatalf("IncrRequest() error = %v", err)
	}

	metrics, err = b.Snapshot(method, t2)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if metrics.AlertCount != 2 || metrics.RequestTotal != 2 || metrics.RequestFailures != 1 {
		t.Fatalf("unexpected metrics after second bucket: %+v", metrics)
	}

	t3 := base.Add(61 * time.Second)
	metrics, err = b.Snapshot(method, t3)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if metrics.AlertCount != 1 || metrics.RequestTotal != 1 || metrics.RequestFailures != 0 {
		t.Fatalf("unexpected metrics after rotation: %+v", metrics)
	}
}
