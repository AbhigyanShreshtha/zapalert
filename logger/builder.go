package logger

import (
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// BuildConfig defines logger construction settings.
type BuildConfig struct {
	Base             *zap.Logger
	ZapConfig        *zap.Config
	ZapOptions       []zap.Option
	MinLevel         zapcore.Level
	OutputPaths      []string
	ErrorOutputPaths []string
	StaticFields     []zap.Field
	IncludeCaller    bool
	CallerKey        string
}

// Build creates a zap logger with JSON output and RFC3339Nano timestamps by default.
func Build(cfg BuildConfig) (*zap.Logger, error) {
	if cfg.Base != nil {
		logger := cfg.Base.WithOptions(cfg.ZapOptions...)
		if len(cfg.StaticFields) > 0 {
			logger = logger.With(cfg.StaticFields...)
		}
		return logger, nil
	}

	encCfg := defaultEncoderConfig(cfg.CallerKey)
	if cfg.ZapConfig != nil {
		encCfg = mergeEncoderConfig(encCfg, cfg.ZapConfig.EncoderConfig)
	}
	if !cfg.IncludeCaller {
		encCfg.CallerKey = ""
	}

	outputs := cfg.OutputPaths
	if len(outputs) == 0 {
		if cfg.ZapConfig != nil && len(cfg.ZapConfig.OutputPaths) > 0 {
			outputs = cfg.ZapConfig.OutputPaths
		} else {
			outputs = []string{"stdout"}
		}
	}
	errorOutputs := cfg.ErrorOutputPaths
	if len(errorOutputs) == 0 {
		if cfg.ZapConfig != nil && len(cfg.ZapConfig.ErrorOutputPaths) > 0 {
			errorOutputs = cfg.ZapConfig.ErrorOutputPaths
		} else {
			errorOutputs = []string{"stderr"}
		}
	}

	ws, err := openPaths(outputs)
	if err != nil {
		return nil, err
	}
	errWS, err := openPaths(errorOutputs)
	if err != nil {
		return nil, err
	}

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encCfg),
		ws,
		cfg.MinLevel,
	)

	options := make([]zap.Option, 0, len(cfg.ZapOptions)+2)
	options = append(options, zap.ErrorOutput(errWS))
	if cfg.IncludeCaller {
		options = append(options, zap.AddCaller())
	}
	options = append(options, cfg.ZapOptions...)

	logger := zap.New(core, options...)
	if len(cfg.StaticFields) > 0 {
		logger = logger.With(cfg.StaticFields...)
	}
	return logger, nil
}

func defaultEncoderConfig(callerKey string) zapcore.EncoderConfig {
	if callerKey == "" {
		callerKey = "caller"
	}
	return zapcore.EncoderConfig{
		TimeKey:          "ts",
		LevelKey:         "level",
		NameKey:          "logger",
		CallerKey:        callerKey,
		MessageKey:       "msg",
		StacktraceKey:    "stacktrace",
		LineEnding:       zapcore.DefaultLineEnding,
		EncodeLevel:      zapcore.LowercaseLevelEncoder,
		EncodeTime:       rfc3339NanoEncoder,
		EncodeDuration:   zapcore.StringDurationEncoder,
		EncodeCaller:     zapcore.ShortCallerEncoder,
		EncodeName:       zapcore.FullNameEncoder,
		ConsoleSeparator: "\t",
	}
}

func rfc3339NanoEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString(t.Format(time.RFC3339Nano))
}

func mergeEncoderConfig(base, override zapcore.EncoderConfig) zapcore.EncoderConfig {
	if override.TimeKey != "" {
		base.TimeKey = override.TimeKey
	}
	if override.LevelKey != "" {
		base.LevelKey = override.LevelKey
	}
	if override.NameKey != "" {
		base.NameKey = override.NameKey
	}
	if override.CallerKey != "" {
		base.CallerKey = override.CallerKey
	}
	if override.MessageKey != "" {
		base.MessageKey = override.MessageKey
	}
	if override.StacktraceKey != "" {
		base.StacktraceKey = override.StacktraceKey
	}
	if override.SkipLineEnding {
		base.SkipLineEnding = true
	}
	if override.LineEnding != "" {
		base.LineEnding = override.LineEnding
	}
	if override.EncodeLevel != nil {
		base.EncodeLevel = override.EncodeLevel
	}
	if override.EncodeTime != nil {
		base.EncodeTime = override.EncodeTime
	}
	if override.EncodeDuration != nil {
		base.EncodeDuration = override.EncodeDuration
	}
	if override.EncodeCaller != nil {
		base.EncodeCaller = override.EncodeCaller
	}
	if override.EncodeName != nil {
		base.EncodeName = override.EncodeName
	}
	if override.NewReflectedEncoder != nil {
		base.NewReflectedEncoder = override.NewReflectedEncoder
	}
	if override.ConsoleSeparator != "" {
		base.ConsoleSeparator = override.ConsoleSeparator
	}
	if override.FunctionKey != "" {
		base.FunctionKey = override.FunctionKey
	}
	if override.EncodeTime != nil {
		base.EncodeTime = override.EncodeTime
	}
	if base.TimeKey == "" {
		base.TimeKey = "ts"
	}
	if base.LevelKey == "" {
		base.LevelKey = "level"
	}
	if base.MessageKey == "" {
		base.MessageKey = "msg"
	}
	return base
}

func openPaths(paths []string) (zapcore.WriteSyncer, error) {
	syncers := make([]zapcore.WriteSyncer, 0, len(paths))
	for _, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			return nil, fmt.Errorf("output path must not be empty")
		}
		switch trimmed {
		case "stdout":
			syncers = append(syncers, zapcore.AddSync(os.Stdout))
		case "stderr":
			syncers = append(syncers, zapcore.AddSync(os.Stderr))
		default:
			f, err := os.OpenFile(trimmed, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				return nil, fmt.Errorf("open %q: %w", trimmed, err)
			}
			syncers = append(syncers, zapcore.AddSync(f))
		}
	}
	if len(syncers) == 0 {
		return nil, fmt.Errorf("at least one output path is required")
	}
	return zapcore.NewMultiWriteSyncer(syncers...), nil
}
