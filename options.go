package zapalert

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/your_github_user_or_org/zapalert/alert"
	"github.com/your_github_user_or_org/zapalert/backend"
	redisbackend "github.com/your_github_user_or_org/zapalert/backend/redis"
	"github.com/your_github_user_or_org/zapalert/ctxmeta"
)

// Option configures logger creation.
type Option func(*options) error

type options struct {
	serviceName       string
	zapLogger         *zap.Logger
	zapConfig         *zap.Config
	zapOptions        []zap.Option
	minLevel          zapcore.Level
	minLevelSet       bool
	outputPaths       []string
	errorOutputPaths  []string
	staticFields      []zap.Field
	contextExtractors []ctxmeta.Extractor
	escalationCfg     *alert.Config
	backend           backend.Backend
	redisCfg          *redisbackend.Config
	defaultAlertLevel AlertLevel
	includeCaller     bool
	callerKey         string
}

func defaultOptions() options {
	return options{
		minLevel:          zapcore.InfoLevel,
		defaultAlertLevel: AlertLevel("NONE"),
		contextExtractors: []ctxmeta.Extractor{ctxmeta.DefaultExtractor},
	}
}

// WithServiceName sets the service name included in every log entry.
func WithServiceName(name string) Option {
	return func(o *options) error {
		o.serviceName = name
		return nil
	}
}

// WithZap uses a pre-built zap logger.
func WithZap(l *zap.Logger) Option {
	return func(o *options) error {
		if l == nil {
			return fmt.Errorf("zap logger must not be nil")
		}
		o.zapLogger = l
		return nil
	}
}

// WithZapConfig sets a zap config used to build the logger.
func WithZapConfig(cfg zap.Config) Option {
	return func(o *options) error {
		copyCfg := cfg
		o.zapConfig = &copyCfg
		return nil
	}
}

// WithZapOptions appends zap options used while constructing the logger.
func WithZapOptions(opts ...zap.Option) Option {
	return func(o *options) error {
		o.zapOptions = append(o.zapOptions, opts...)
		return nil
	}
}

// WithMinLevel sets the minimum enabled log level.
func WithMinLevel(level zapcore.Level) Option {
	return func(o *options) error {
		o.minLevel = level
		o.minLevelSet = true
		return nil
	}
}

// WithOutputPaths sets output destinations for regular logs.
func WithOutputPaths(paths []string) Option {
	return func(o *options) error {
		if len(paths) == 0 {
			return fmt.Errorf("output paths must not be empty")
		}
		o.outputPaths = append([]string(nil), paths...)
		return nil
	}
}

// WithErrorOutputPaths sets output destinations for internal logger errors.
func WithErrorOutputPaths(paths []string) Option {
	return func(o *options) error {
		if len(paths) == 0 {
			return fmt.Errorf("error output paths must not be empty")
		}
		o.errorOutputPaths = append([]string(nil), paths...)
		return nil
	}
}

// WithStaticFields adds static top-level fields to every log entry.
func WithStaticFields(fields map[string]any) Option {
	return func(o *options) error {
		if len(fields) == 0 {
			return nil
		}
		for key, val := range fields {
			if key == "" {
				return fmt.Errorf("static field key must not be empty")
			}
			o.staticFields = append(o.staticFields, zap.Any(key, val))
		}
		return nil
	}
}

// WithStaticZapFields adds static zap fields to every log entry.
func WithStaticZapFields(fields ...zap.Field) Option {
	return func(o *options) error {
		o.staticFields = append(o.staticFields, fields...)
		return nil
	}
}

// WithContextExtractors appends context metadata extractors.
func WithContextExtractors(extractors ...ctxmeta.Extractor) Option {
	return func(o *options) error {
		for i, ex := range extractors {
			if ex == nil {
				return fmt.Errorf("context extractor at index %d is nil", i)
			}
			o.contextExtractors = append(o.contextExtractors, ex)
		}
		return nil
	}
}

// WithEscalation enables alert escalation with the provided config.
func WithEscalation(cfg alert.Config) Option {
	return func(o *options) error {
		copyCfg := cfg
		o.escalationCfg = &copyCfg
		return nil
	}
}

// WithBackend sets a custom backend for escalation metrics.
func WithBackend(b backend.Backend) Option {
	return func(o *options) error {
		if b == nil {
			return fmt.Errorf("backend must not be nil")
		}
		o.backend = b
		return nil
	}
}

// WithRedisBackend configures and builds the Redis backend during New().
func WithRedisBackend(cfg redisbackend.Config) Option {
	return func(o *options) error {
		copyCfg := cfg
		o.redisCfg = &copyCfg
		return nil
	}
}

// WithDefaultAlertLevel sets the fallback alert level when escalation is disabled or no base level is supplied.
func WithDefaultAlertLevel(level AlertLevel) Option {
	return func(o *options) error {
		if level == "" {
			return fmt.Errorf("default alert level must not be empty")
		}
		o.defaultAlertLevel = level
		return nil
	}
}

// WithCaller enables caller information in output logs.
func WithCaller(enable bool) Option {
	return func(o *options) error {
		o.includeCaller = enable
		return nil
	}
}

// WithCallerKey sets the caller field key.
func WithCallerKey(key string) Option {
	return func(o *options) error {
		o.callerKey = key
		return nil
	}
}
