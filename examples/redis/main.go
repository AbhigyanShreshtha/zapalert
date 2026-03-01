package main

import (
	"context"
	"errors"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/your_github_user_or_org/zapalert"
	"github.com/your_github_user_or_org/zapalert/alert"
	redisbackend "github.com/your_github_user_or_org/zapalert/backend/redis"
	"github.com/your_github_user_or_org/zapalert/ctxmeta"
)

func main() {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	log, err := zapalert.New(
		zapalert.WithServiceName("payments-service"),
		zapalert.WithDefaultAlertLevel("NONE"),
		zapalert.WithEscalation(alert.Config{
			Enabled:               true,
			Window:                time.Minute,
			BucketCount:           6,
			DefaultBaseAlertLevel: "P3-Low",
			ErrorBaseAlertLevel:   "P3-Low",
			Ladder:                []alert.AlertLevel{"P3-Low", "P2-Mid", "P1-High"},
			Rules: []alert.Rule{
				{
					MethodPattern:       "^ChargeCard$",
					CountThresholds:     map[alert.AlertLevel]int{"P3-Low": 100, "P2-Mid": 300},
					PercentThresholds:   map[alert.AlertLevel]float64{"P2-Mid": 0.20},
					MinimumRequestCount: 100,
					Cooldown:            5 * time.Minute,
					Deescalate:          true,
				},
			},
			SnapshotCacheTTL: 2 * time.Second,
		}),
		zapalert.WithRedisBackend(redisbackend.Config{
			Addr:        redisAddr,
			Service:     "payments-service",
			Prefix:      "zapalert",
			Window:      time.Minute,
			BucketCount: 6,
		}),
	)
	if err != nil {
		panic(err)
	}
	defer log.Sync()

	ctx := ctxmeta.WithRequestID(context.Background(), "req-redis-001")
	ctx = ctxmeta.WithClientID(ctx, "merchant-17")

	for i := 0; i < 20; i++ {
		success := i%5 != 0
		log.ObserveRequest(ctx, "ChargeCard", success)
		if !success {
			log.Error(ctx, "ChargeCard", errors.New("gateway timeout"), "charge failed", zap.Int("attempt", i+1))
		}
	}

	log.Alert(ctx, "ChargeCard", "P3-Low", "manual alert example", zap.String("source", "redis-example"))
}
