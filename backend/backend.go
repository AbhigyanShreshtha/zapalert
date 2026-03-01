package backend

import (
	"time"

	"github.com/your_github_user_or_org/zapalert/internal/level"
)

// Metrics holds rolling-window counters used by escalation rules.
type Metrics struct {
	AlertCount      int
	RequestTotal    int
	RequestFailures int
	FailureRate     float64
}

// Backend stores and retrieves rolling metrics for methods.
type Backend interface {
	IncrAlert(method string, base level.AlertLevel, at time.Time) error
	IncrRequest(method string, success bool, at time.Time) error
	Snapshot(method string, at time.Time) (Metrics, error)
}
