# zapalert

`zapalert` is a production-grade Go logging library built on top of Uber [zap](https://github.com/uber-go/zap) that adds:

- A consistent structured JSON log schema.
- Configurable alert escalation rules.
- Optional Redis-backed rolling metrics for multi-instance deployments.
- Context metadata extraction for request-level observability.

Module path:

```bash
github.com/AbhigyanShreshtha/zapalert
```

## Why zapalert

`zapalert` keeps logging fast and structured while adding escalation logic you can tune per method/endpoint.
It is designed for:

- Single-service local development (in-memory rolling counters).
- Multi-pod production systems (Redis rolling counters).
- Count-based and percentage-based escalation.

## Features

- Built on `go.uber.org/zap` + `zapcore`.
- JSON logs by default.
- RFC3339Nano timestamps with timezone (`ts`).
- Stable schema keys: `ts`, `level`, `alert_level`, `msg`, `err`, `service`, `method`, `request_id`.
- Custom context metadata extractors.
- Strict escalation config validation.
- Thread-safe logger and backend implementations.

## Installation

```bash
go get github.com/AbhigyanShreshtha/zapalert
```

## Log schema

Default JSON keys:

- `ts` (RFC3339Nano with timezone)
- `level` (`debug`/`info`/`warn`/`error`)
- `alert_level` (string)
- `msg` (message)
- `err` (error string; omitted when no error)
- `service`
- `method`
- `request_id` (if present in context)
- user fields (`zap.Field`) at top level

Example:

```json
{
  "ts": "2026-03-01T12:34:56.123456789+05:30",
  "level": "error",
  "msg": "request failed",
  "alert_level": "P2-Mid",
  "err": "gateway timeout",
  "service": "payments-service",
  "method": "ChargeCard",
  "request_id": "req-001",
  "client_id": "merchant-42",
  "ip": "203.0.113.10",
  "latency_ms": 182
}
```

## Quickstart (No Escalation)

```go
package main

import (
	"context"
	"errors"

	"go.uber.org/zap"

	"github.com/AbhigyanShreshtha/zapalert"
	"github.com/AbhigyanShreshtha/zapalert/ctxmeta"
)

func main() {
	log, err := zapalert.New(
		zapalert.WithServiceName("kyc-service"),
	)
	if err != nil {
		panic(err)
	}
	defer log.Sync()

	ctx := ctxmeta.WithRequestID(context.Background(), "req-123")
	log.Info(ctx, "KYC.Submit", "request accepted", zap.String("stage", "validation"))
	log.Error(ctx, "KYC.Submit", errors.New("document missing"), "request failed")
}
```

Runnable example: [`examples/basic/main.go`](./examples/basic/main.go)

## Escalation model

Escalation is configured via `alert.Config` and method-specific `alert.Rule`.

Method matching uses **regular expressions** (`Rule.MethodPattern`).

### Rule behavior

- Count thresholds (`CountThresholds`) escalate when rolling alert count crosses threshold.
- Percentage thresholds (`PercentThresholds`) escalate when failure rate crosses threshold and `MinimumRequestCount` is satisfied.
- Escalation target is the next step in `Ladder` from the threshold key.
- `Cooldown` delays de-escalation after an escalation.
- `Deescalate=false` keeps an escalated level from dropping during the configured window.

## Escalation without Redis (In-memory)

```go
cfg := alert.Config{
	Enabled:               true,
	Window:                time.Minute,
	BucketCount:           6,
	DefaultBaseAlertLevel: "P3-Low",
	ErrorBaseAlertLevel:   "P3-Low",
	Ladder:                []alert.AlertLevel{"P3-Low", "P2-Mid", "P1-High"},
	Rules: []alert.Rule{
		{
			MethodPattern:   "^ChargeCard$",
			CountThresholds: map[alert.AlertLevel]int{"P3-Low": 100, "P2-Mid": 300},
			Cooldown:        5 * time.Minute,
			Deescalate:      true,
		},
	},
}

log, err := zapalert.New(
	zapalert.WithServiceName("payments-service"),
	zapalert.WithEscalation(cfg), // uses in-memory backend by default
)
```

## Escalation with Redis (Multi-pod)

```go
log, err := zapalert.New(
	zapalert.WithServiceName("payments-service"),
	zapalert.WithEscalation(alert.Config{
		Enabled:               true,
		Window:                time.Minute,
		BucketCount:           6,
		DefaultBaseAlertLevel: "P3-Low",
		ErrorBaseAlertLevel:   "P3-Low",
		Ladder:                []alert.AlertLevel{"P3-Low", "P2-Mid", "P1-High"},
		Rules: []alert.Rule{
			{
				MethodPattern:   "^ChargeCard$",
				CountThresholds: map[alert.AlertLevel]int{"P3-Low": 100, "P2-Mid": 300},
			},
		},
		SnapshotCacheTTL: 2 * time.Second,
	}),
	zapalert.WithRedisBackend(redis.Config{
		Addr:        "localhost:6379",
		Service:     "payments-service",
		Prefix:      "zapalert",
		Window:      time.Minute,
		BucketCount: 6,
	}),
)
```

Redis key pattern:

- `zapalert:{service}:{method}:req:{bucket}` (hash fields `total`, `fail`)
- `zapalert:{service}:{method}:alert:{bucket}` (integer)

Each bucket key gets `EXPIRE = Window + 2*bucketWidth`.

Runnable example: [`examples/redis/main.go`](./examples/redis/main.go)

## Percentage escalation

Use `ObserveRequest` for request accounting and `Error`/`Alert` for alert events.

```go
log.ObserveRequest(ctx, "GET /charge", success)
if !success {
	log.Error(ctx, "GET /charge", err, "request failed")
}
```

Example rule:

```go
alert.Rule{
	MethodPattern:       "^GET /charge$",
	PercentThresholds:   map[alert.AlertLevel]float64{"P2-Mid": 0.20},
	MinimumRequestCount: 100,
	Deescalate:          true,
}
```

Runnable middleware example: [`examples/http_middleware/main.go`](./examples/http_middleware/main.go)

## Setup samples

### 1) No Redis, no escalation (basic logger)

```go
log, err := zapalert.New(
	zapalert.WithServiceName("kyc-service"),
)
```

### 2) No Redis, with escalation (local dev)

```go
log, err := zapalert.New(
	zapalert.WithServiceName("payments-service"),
	zapalert.WithEscalation(escalationCfg),
)
```

### 3) With Redis, with escalation (prod)

```go
log, err := zapalert.New(
	zapalert.WithServiceName("payments-service"),
	zapalert.WithEscalation(escalationCfg),
	zapalert.WithRedisBackend(redisCfg),
)
```

### 4) With Redis, escalation only count-based

```go
countOnlyCfg := escalationCfg
countOnlyCfg.Rules = []alert.Rule{
	{
		MethodPattern:   "^ChargeCard$",
		CountThresholds: map[alert.AlertLevel]int{"P3-Low": 100, "P2-Mid": 300},
	},
}
```

### 5) With Redis, escalation only percent-based

```go
percentOnlyCfg := escalationCfg
percentOnlyCfg.Rules = []alert.Rule{
	{
		MethodPattern:       "^ChargeCard$",
		PercentThresholds:   map[alert.AlertLevel]float64{"P2-Mid": 0.20},
		MinimumRequestCount: 100,
	},
}
```

## Context metadata (`ctxmeta`)

Built-in helpers:

- `ctxmeta.WithRequestID(ctx, id)`
- `ctxmeta.WithClientID(ctx, id)`
- `ctxmeta.WithIP(ctx, ip)`
- `ctxmeta.WithUserAgent(ctx, ua)`

Default extractor emits:

- `request_id`
- `client_id`
- `ip`
- `user_agent`

You can provide custom extractors via `WithContextExtractors(...)`.

## Public API summary

```go
type AlertLevel string
type Field = zap.Field

type Logger interface {
	Debug(ctx context.Context, method string, msg string, fields ...Field)
	Info(ctx context.Context, method string, msg string, fields ...Field)
	Warn(ctx context.Context, method string, msg string, fields ...Field)
	Error(ctx context.Context, method string, err error, msg string, fields ...Field)
	Alert(ctx context.Context, method string, base AlertLevel, msg string, fields ...Field)
	ObserveRequest(ctx context.Context, method string, success bool, fields ...Field)
	Sync() error
}

func New(opts ...Option) (Logger, error)
```

Core options:

- `WithServiceName(string)`
- `WithZap(*zap.Logger)`
- `WithZapConfig(zap.Config)`
- `WithZapOptions(...zap.Option)`
- `WithMinLevel(zapcore.Level)`
- `WithOutputPaths([]string)`
- `WithErrorOutputPaths([]string)`
- `WithStaticFields(map[string]any)`
- `WithStaticZapFields(...zap.Field)`
- `WithContextExtractors(...ctxmeta.Extractor)`
- `WithEscalation(alert.Config)`
- `WithBackend(backend.Backend)`
- `WithRedisBackend(redis.Config)`

## Validation and defaults

- `service_name` is required (`WithServiceName`).
- Escalation validation is strict (`Window`, `BucketCount`, `Ladder`, thresholds, regex patterns).
- Defaults:
  - JSON logs
  - stdout/stderr sinks
  - `alert_level="NONE"` when escalation is disabled (or when no base is given)
  - in-memory backend when escalation is enabled and no backend is configured

## Running examples

```bash
go run ./examples/basic
go run ./examples/http_middleware
REDIS_ADDR=localhost:6379 go run ./examples/redis
```

## Testing

```bash
go test ./...
```

Includes tests for:

- in-memory sliding-window rotation correctness
- count threshold escalation
- percentage threshold escalation with minimum sample
- cooldown and optional de-escalation behavior
- Redis keying and rolling snapshot correctness
