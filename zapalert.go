package zapalert

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/your_github_user_or_org/zapalert/alert"
	"github.com/your_github_user_or_org/zapalert/backend"
	"github.com/your_github_user_or_org/zapalert/backend/inmem"
	redisbackend "github.com/your_github_user_or_org/zapalert/backend/redis"
	"github.com/your_github_user_or_org/zapalert/ctxmeta"
	"github.com/your_github_user_or_org/zapalert/internal/level"
	logbuilder "github.com/your_github_user_or_org/zapalert/logger"
)

// AlertLevel is a user-defined alert severity label.
type AlertLevel = level.AlertLevel

// Field is a convenience alias for zap.Field.
type Field = zap.Field

// Logger exposes structured logging and alert escalation methods.
type Logger interface {
	Debug(ctx context.Context, method string, msg string, fields ...Field)
	Info(ctx context.Context, method string, msg string, fields ...Field)
	Warn(ctx context.Context, method string, msg string, fields ...Field)
	Error(ctx context.Context, method string, err error, msg string, fields ...Field)
	Alert(ctx context.Context, method string, base AlertLevel, msg string, fields ...Field)
	ObserveRequest(ctx context.Context, method string, success bool, fields ...Field)
	Sync() error
}

type loggerImpl struct {
	serviceName       string
	logger            *zap.Logger
	extractors        []ctxmeta.Extractor
	escalation        *alert.Engine
	defaultAlertLevel AlertLevel
	errorBaseLevel    AlertLevel
	now               func() time.Time
}

// New creates a new zapalert logger.
func New(opts ...Option) (Logger, error) {
	cfg := defaultOptions()
	for i, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("option at index %d is nil", i)
		}
		if err := opt(&cfg); err != nil {
			return nil, fmt.Errorf("apply option %d: %w", i, err)
		}
	}

	if cfg.serviceName == "" {
		return nil, fmt.Errorf("service name is required")
	}
	if cfg.zapLogger != nil && cfg.zapConfig != nil {
		return nil, fmt.Errorf("WithZap and WithZapConfig are mutually exclusive")
	}
	if cfg.backend != nil && cfg.redisCfg != nil {
		return nil, fmt.Errorf("WithBackend and WithRedisBackend are mutually exclusive")
	}

	minLevel := cfg.minLevel
	if cfg.zapConfig != nil && !cfg.minLevelSet {
		minLevel = cfg.zapConfig.Level.Level()
	}

	zlogger, err := logbuilder.Build(logbuilder.BuildConfig{
		Base:             cfg.zapLogger,
		ZapConfig:        cfg.zapConfig,
		ZapOptions:       cfg.zapOptions,
		MinLevel:         minLevel,
		OutputPaths:      cfg.outputPaths,
		ErrorOutputPaths: cfg.errorOutputPaths,
		StaticFields:     cfg.staticFields,
		IncludeCaller:    cfg.includeCaller,
		CallerKey:        cfg.callerKey,
	})
	if err != nil {
		return nil, fmt.Errorf("build zap logger: %w", err)
	}

	var engine *alert.Engine
	errorBase := cfg.defaultAlertLevel
	if cfg.escalationCfg != nil && cfg.escalationCfg.Enabled {
		escalationCfg := *cfg.escalationCfg
		if escalationCfg.DefaultBaseAlertLevel == "" {
			escalationCfg.DefaultBaseAlertLevel = alert.AlertLevel(cfg.defaultAlertLevel)
		}
		if escalationCfg.ErrorBaseAlertLevel == "" {
			escalationCfg.ErrorBaseAlertLevel = escalationCfg.DefaultBaseAlertLevel
		}
		errorBase = AlertLevel(escalationCfg.ErrorBaseAlertLevel)

		metricsBackend, err := buildBackend(cfg, escalationCfg)
		if err != nil {
			return nil, err
		}
		engine, err = alert.NewEngine(escalationCfg, metricsBackend)
		if err != nil {
			return nil, fmt.Errorf("create escalation engine: %w", err)
		}
	}

	if len(cfg.contextExtractors) == 0 {
		cfg.contextExtractors = []ctxmeta.Extractor{ctxmeta.DefaultExtractor}
	}

	return &loggerImpl{
		serviceName:       cfg.serviceName,
		logger:            zlogger,
		extractors:        cfg.contextExtractors,
		escalation:        engine,
		defaultAlertLevel: cfg.defaultAlertLevel,
		errorBaseLevel:    errorBase,
		now:               time.Now,
	}, nil
}

func buildBackend(cfg options, escalationCfg alert.Config) (backend.Backend, error) {
	if cfg.backend != nil {
		return cfg.backend, nil
	}
	if cfg.redisCfg != nil {
		redisCfg := *cfg.redisCfg
		if redisCfg.Service == "" {
			redisCfg.Service = cfg.serviceName
		}
		if redisCfg.Window <= 0 {
			redisCfg.Window = escalationCfg.Window
		}
		if redisCfg.BucketCount <= 0 {
			redisCfg.BucketCount = escalationCfg.BucketCount
		}
		b, backendErr := redisbackend.New(redisCfg)
		if backendErr != nil {
			return nil, fmt.Errorf("create redis backend: %w", backendErr)
		}
		return b, nil
	}
	b, backendErr := inmem.New(inmem.Config{
		Window:      escalationCfg.Window,
		BucketCount: escalationCfg.BucketCount,
		MethodTTL:   2 * escalationCfg.Window,
	})
	if backendErr != nil {
		return nil, fmt.Errorf("create in-memory backend: %w", backendErr)
	}
	return b, nil
}

// Debug logs a debug message.
func (l *loggerImpl) Debug(ctx context.Context, method string, msg string, fields ...Field) {
	l.log(ctx, zapcore.DebugLevel, method, l.defaultAlertLevel, nil, msg, fields...)
}

// Info logs an info message.
func (l *loggerImpl) Info(ctx context.Context, method string, msg string, fields ...Field) {
	l.log(ctx, zapcore.InfoLevel, method, l.defaultAlertLevel, nil, msg, fields...)
}

// Warn logs a warning message.
func (l *loggerImpl) Warn(ctx context.Context, method string, msg string, fields ...Field) {
	l.log(ctx, zapcore.WarnLevel, method, l.defaultAlertLevel, nil, msg, fields...)
}

// Error logs an error message and evaluates escalation.
func (l *loggerImpl) Error(ctx context.Context, method string, err error, msg string, fields ...Field) {
	levelToUse := l.errorBaseLevel
	if l.escalation != nil {
		effective, evalErr := l.escalation.RecordAlert(method, alert.AlertLevel(l.errorBaseLevel), l.now())
		if evalErr != nil {
			fields = append(fields, zap.String("escalation_error", evalErr.Error()))
		} else {
			levelToUse = AlertLevel(effective)
		}
	}
	l.log(ctx, zapcore.ErrorLevel, method, levelToUse, err, msg, fields...)
}

// Alert records an explicit alert event and logs at warn level.
func (l *loggerImpl) Alert(ctx context.Context, method string, base AlertLevel, msg string, fields ...Field) {
	if base == "" {
		base = l.defaultAlertLevel
	}
	levelToUse := base
	if l.escalation != nil {
		effective, err := l.escalation.RecordAlert(method, alert.AlertLevel(base), l.now())
		if err != nil {
			fields = append(fields, zap.String("escalation_error", err.Error()))
		} else {
			levelToUse = AlertLevel(effective)
		}
	}
	l.log(ctx, zapcore.WarnLevel, method, levelToUse, nil, msg, fields...)
}

// ObserveRequest records request success/failure for percentage-based escalation.
func (l *loggerImpl) ObserveRequest(_ context.Context, method string, success bool, _ ...Field) {
	if l.escalation == nil {
		return
	}
	if err := l.escalation.ObserveRequest(method, success, l.now()); err != nil {
		l.log(context.Background(), zapcore.WarnLevel, method, l.defaultAlertLevel, err, "failed to observe request metrics")
	}
}

// Sync flushes buffered log entries.
func (l *loggerImpl) Sync() error {
	return l.logger.Sync()
}

func (l *loggerImpl) log(ctx context.Context, level zapcore.Level, method string, alertLevel AlertLevel, err error, msg string, fields ...Field) {
	if ctx == nil {
		ctx = context.Background()
	}

	entryMethod := method
	if entryMethod == "" {
		entryMethod = "unknown"
	}

	allFields := make([]zap.Field, 0, len(fields)+4)
	allFields = append(allFields,
		zap.String("service", l.serviceName),
		zap.String("method", entryMethod),
		zap.String("alert_level", string(alertLevel)),
	)
	for _, ex := range l.extractors {
		if ex == nil {
			continue
		}
		allFields = append(allFields, ex(ctx)...)
	}
	if err != nil {
		allFields = append(allFields, zap.NamedError("err", err))
	}
	allFields = append(allFields, fields...)

	if ce := l.logger.Check(level, msg); ce != nil {
		ce.Write(allFields...)
	}
}
