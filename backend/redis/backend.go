package redis

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/your_github_user_or_org/zapalert/backend"
	"github.com/your_github_user_or_org/zapalert/internal/level"
)

// Config controls the Redis backend.
type Config struct {
	Addr             string
	Username         string
	Password         string
	DB               int
	Service          string
	Prefix           string
	Window           time.Duration
	BucketCount      int
	OperationTimeout time.Duration
	Client           *redis.Client
}

// Backend is a Redis-backed implementation of backend.Backend.
type Backend struct {
	client            *redis.Client
	service           string
	prefix            string
	window            time.Duration
	bucketCnt         int
	bucketWidth       time.Duration
	bucketWidthSecond int64
	expire            time.Duration
	opTimeout         time.Duration
	ownsClient        bool
}

var _ backend.Backend = (*Backend)(nil)

// New creates a Redis backend.
func New(cfg Config) (*Backend, error) {
	if cfg.Service == "" {
		return nil, fmt.Errorf("service must not be empty")
	}
	if cfg.Window <= 0 {
		return nil, fmt.Errorf("window must be > 0")
	}
	if cfg.BucketCount <= 0 {
		return nil, fmt.Errorf("bucket count must be > 0")
	}

	bucketWidth := cfg.Window / time.Duration(cfg.BucketCount)
	if bucketWidth <= 0 {
		return nil, fmt.Errorf("bucket size must be > 0; increase window or reduce bucket count")
	}
	if bucketWidth < time.Second {
		return nil, fmt.Errorf("bucket size must be >= 1s for redis backend")
	}

	prefix := cfg.Prefix
	if prefix == "" {
		prefix = "zapalert"
	}
	timeout := cfg.OperationTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	client := cfg.Client
	ownsClient := false
	if client == nil {
		if cfg.Addr == "" {
			return nil, fmt.Errorf("addr must not be empty when client is nil")
		}
		client = redis.NewClient(&redis.Options{
			Addr:     cfg.Addr,
			Username: cfg.Username,
			Password: cfg.Password,
			DB:       cfg.DB,
		})
		ownsClient = true
	}

	expire := cfg.Window + 2*bucketWidth

	return &Backend{
		client:            client,
		service:           cfg.Service,
		prefix:            prefix,
		window:            cfg.Window,
		bucketCnt:         cfg.BucketCount,
		bucketWidth:       bucketWidth,
		bucketWidthSecond: int64(bucketWidth / time.Second),
		expire:            expire,
		opTimeout:         timeout,
		ownsClient:        ownsClient,
	}, nil
}

// Close closes the underlying Redis client if this backend created it.
func (b *Backend) Close() error {
	if !b.ownsClient {
		return nil
	}
	return b.client.Close()
}

// IncrAlert increments the alert counter in the current time bucket.
func (b *Backend) IncrAlert(method string, _ level.AlertLevel, at time.Time) error {
	if method == "" {
		return fmt.Errorf("method must not be empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.opTimeout)
	defer cancel()

	bucket := b.bucketAt(at)
	key := b.alertKey(method, bucket)

	pipe := b.client.Pipeline()
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, b.expire)
	_, err := pipe.Exec(ctx)
	return err
}

// IncrRequest increments request totals and failures in the current time bucket.
func (b *Backend) IncrRequest(method string, success bool, at time.Time) error {
	if method == "" {
		return fmt.Errorf("method must not be empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.opTimeout)
	defer cancel()

	bucket := b.bucketAt(at)
	key := b.reqKey(method, bucket)

	pipe := b.client.Pipeline()
	pipe.HIncrBy(ctx, key, "total", 1)
	if !success {
		pipe.HIncrBy(ctx, key, "fail", 1)
	}
	pipe.Expire(ctx, key, b.expire)
	_, err := pipe.Exec(ctx)
	return err
}

// Snapshot returns rolling-window metrics for a method by summing recent buckets.
func (b *Backend) Snapshot(method string, at time.Time) (backend.Metrics, error) {
	if method == "" {
		return backend.Metrics{}, fmt.Errorf("method must not be empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.opTimeout)
	defer cancel()

	currentBucket := b.bucketAt(at)
	startBucket := currentBucket - int64(b.bucketCnt) + 1

	type bucketCmds struct {
		alert *redis.StringCmd
		total *redis.StringCmd
		fail  *redis.StringCmd
	}
	cmds := make([]bucketCmds, 0, b.bucketCnt)

	pipe := b.client.Pipeline()
	for bucket := startBucket; bucket <= currentBucket; bucket++ {
		alertKey := b.alertKey(method, bucket)
		reqKey := b.reqKey(method, bucket)
		cmds = append(cmds, bucketCmds{
			alert: pipe.Get(ctx, alertKey),
			total: pipe.HGet(ctx, reqKey, "total"),
			fail:  pipe.HGet(ctx, reqKey, "fail"),
		})
	}
	_, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return backend.Metrics{}, err
	}

	metrics := backend.Metrics{}
	for _, c := range cmds {
		alerts, err := parseInt(c.alert)
		if err != nil {
			return backend.Metrics{}, err
		}
		total, err := parseInt(c.total)
		if err != nil {
			return backend.Metrics{}, err
		}
		fail, err := parseInt(c.fail)
		if err != nil {
			return backend.Metrics{}, err
		}
		metrics.AlertCount += alerts
		metrics.RequestTotal += total
		metrics.RequestFailures += fail
	}
	if metrics.RequestTotal > 0 {
		metrics.FailureRate = float64(metrics.RequestFailures) / float64(metrics.RequestTotal)
	}
	return metrics, nil
}

func (b *Backend) bucketAt(at time.Time) int64 {
	return at.Unix() / b.bucketWidthSecond
}

func (b *Backend) reqKey(method string, bucket int64) string {
	return fmt.Sprintf("%s:{%s}:%s:req:%d", b.prefix, b.service, method, bucket)
}

func (b *Backend) alertKey(method string, bucket int64) string {
	return fmt.Sprintf("%s:{%s}:%s:alert:%d", b.prefix, b.service, method, bucket)
}

func parseInt(cmd *redis.StringCmd) (int, error) {
	if cmd == nil {
		return 0, nil
	}
	val, err := cmd.Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return 0, err
	}
	return n, nil
}
