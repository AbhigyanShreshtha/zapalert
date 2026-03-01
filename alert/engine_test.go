package alert

import (
	"testing"
	"time"

	"github.com/your_github_user_or_org/zapalert/backend/inmem"
)

func TestCountThresholdEscalation(t *testing.T) {
	engine := newTestEngine(t, Config{
		Enabled:               true,
		Window:                time.Minute,
		BucketCount:           6,
		DefaultBaseAlertLevel: AlertLevel("P3"),
		Ladder:                []AlertLevel{"P3", "P2", "P1"},
		Rules: []Rule{
			{
				MethodPattern:   "^payments$",
				CountThresholds: map[AlertLevel]int{"P3": 2, "P2": 4},
			},
		},
	})

	baseTime := time.Unix(1_700_000_000, 0).UTC()
	levels := make([]AlertLevel, 0, 4)
	for i := 0; i < 4; i++ {
		lvl, err := engine.RecordAlert("payments", "P3", baseTime.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatalf("RecordAlert() error = %v", err)
		}
		levels = append(levels, lvl)
	}

	want := []AlertLevel{"P3", "P2", "P2", "P1"}
	for i := range want {
		if levels[i] != want[i] {
			t.Fatalf("level[%d] = %q, want %q", i, levels[i], want[i])
		}
	}
}

func TestPercentageEscalationWithMinimumSample(t *testing.T) {
	engine := newTestEngine(t, Config{
		Enabled:               true,
		Window:                time.Minute,
		BucketCount:           6,
		DefaultBaseAlertLevel: AlertLevel("P2"),
		Ladder:                []AlertLevel{"P3", "P2", "P1"},
		Rules: []Rule{
			{
				MethodPattern:       "^login$",
				PercentThresholds:   map[AlertLevel]float64{"P2": 0.20},
				MinimumRequestCount: 10,
				Deescalate:          true,
			},
		},
	})

	baseTime := time.Unix(1_700_000_100, 0).UTC()
	for i := 0; i < 9; i++ {
		success := i >= 3
		if err := engine.ObserveRequest("login", success, baseTime.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("ObserveRequest() error = %v", err)
		}
	}

	levelBeforeMin, err := engine.RecordAlert("login", "P2", baseTime.Add(10*time.Second))
	if err != nil {
		t.Fatalf("RecordAlert() before min sample error = %v", err)
	}
	if levelBeforeMin != "P2" {
		t.Fatalf("level before min sample = %q, want P2", levelBeforeMin)
	}

	if err := engine.ObserveRequest("login", true, baseTime.Add(11*time.Second)); err != nil {
		t.Fatalf("ObserveRequest() error = %v", err)
	}

	levelAfterMin, err := engine.RecordAlert("login", "P2", baseTime.Add(12*time.Second))
	if err != nil {
		t.Fatalf("RecordAlert() after min sample error = %v", err)
	}
	if levelAfterMin != "P1" {
		t.Fatalf("level after min sample = %q, want P1", levelAfterMin)
	}
}

func TestCooldownAndDeescalation(t *testing.T) {
	engine := newTestEngine(t, Config{
		Enabled:               true,
		Window:                30 * time.Second,
		BucketCount:           6,
		DefaultBaseAlertLevel: AlertLevel("P3"),
		Ladder:                []AlertLevel{"P3", "P2", "P1"},
		Rules: []Rule{
			{
				MethodPattern:   "^checkout$",
				CountThresholds: map[AlertLevel]int{"P3": 2},
				Cooldown:        time.Minute,
				Deescalate:      true,
			},
		},
	})

	baseTime := time.Unix(1_700_000_200, 0).UTC()
	level1, err := engine.RecordAlert("checkout", "P3", baseTime)
	if err != nil {
		t.Fatalf("RecordAlert() error = %v", err)
	}
	if level1 != "P3" {
		t.Fatalf("first level = %q, want P3", level1)
	}

	level2, err := engine.RecordAlert("checkout", "P3", baseTime.Add(time.Second))
	if err != nil {
		t.Fatalf("RecordAlert() error = %v", err)
	}
	if level2 != "P2" {
		t.Fatalf("second level = %q, want P2", level2)
	}

	level3, err := engine.RecordAlert("checkout", "P3", baseTime.Add(40*time.Second))
	if err != nil {
		t.Fatalf("RecordAlert() error = %v", err)
	}
	if level3 != "P2" {
		t.Fatalf("third level during cooldown = %q, want P2", level3)
	}

	level4, err := engine.RecordAlert("checkout", "P3", baseTime.Add(70*time.Second))
	if err != nil {
		t.Fatalf("RecordAlert() error = %v", err)
	}
	if level4 != "P3" {
		t.Fatalf("fourth level after cooldown = %q, want P3", level4)
	}
}

func TestNoDeescalationWithinWindow(t *testing.T) {
	engine := newTestEngine(t, Config{
		Enabled:               true,
		Window:                time.Minute,
		BucketCount:           6,
		DefaultBaseAlertLevel: AlertLevel("P2"),
		Ladder:                []AlertLevel{"P3", "P2", "P1"},
		Rules: []Rule{
			{
				MethodPattern:       "^search$",
				PercentThresholds:   map[AlertLevel]float64{"P2": 0.50},
				MinimumRequestCount: 2,
				Deescalate:          false,
			},
		},
	})

	baseTime := time.Unix(1_700_000_300, 0).UTC()
	if err := engine.ObserveRequest("search", false, baseTime); err != nil {
		t.Fatalf("ObserveRequest() error = %v", err)
	}
	if err := engine.ObserveRequest("search", false, baseTime.Add(time.Second)); err != nil {
		t.Fatalf("ObserveRequest() error = %v", err)
	}

	escalated, err := engine.RecordAlert("search", "P2", baseTime.Add(2*time.Second))
	if err != nil {
		t.Fatalf("RecordAlert() error = %v", err)
	}
	if escalated != "P1" {
		t.Fatalf("escalated level = %q, want P1", escalated)
	}

	for i := 0; i < 8; i++ {
		if err := engine.ObserveRequest("search", true, baseTime.Add(time.Duration(3+i)*time.Second)); err != nil {
			t.Fatalf("ObserveRequest() error = %v", err)
		}
	}

	stillEscalated, err := engine.RecordAlert("search", "P2", baseTime.Add(13*time.Second))
	if err != nil {
		t.Fatalf("RecordAlert() error = %v", err)
	}
	if stillEscalated != "P1" {
		t.Fatalf("level after recovery within window = %q, want P1", stillEscalated)
	}
}

func newTestEngine(t *testing.T, cfg Config) *Engine {
	t.Helper()

	b, err := inmem.New(inmem.Config{
		Window:      cfg.Window,
		BucketCount: cfg.BucketCount,
		MethodTTL:   2 * cfg.Window,
	})
	if err != nil {
		t.Fatalf("inmem.New() error = %v", err)
	}

	engine, err := NewEngine(cfg, b)
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	return engine
}
