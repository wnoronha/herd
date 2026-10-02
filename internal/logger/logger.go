package logger

import (
	"fmt"
	"log"
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logf is the standard Tailscale / Tailcat printf-style logging function signature.
type Logf func(format string, args ...any)

// Logger defines the unified structured and leveled logging interface across Herd.
type Logger interface {
	Debug(msg string, fields ...zap.Field)
	Info(msg string, fields ...zap.Field)
	Warn(msg string, fields ...zap.Field)
	Error(msg string, fields ...zap.Field)

	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
	Printf(format string, args ...any)

	With(fields ...zap.Field) Logger
	Named(component string) Logger
	WithNode(nodeName string) Logger
	WithComponent(component string) Logger
	WithPeer(peer string) Logger

	Desugar() *zap.Logger
	Sugar() *zap.SugaredLogger
	ToStdLogger() *log.Logger
	Logf() Logf
	Sync() error
}

type zapWrapper struct {
	z *zap.Logger
	s *zap.SugaredLogger
}

func (w *zapWrapper) Debug(msg string, fields ...zap.Field) {
	w.z.Debug(msg, fields...)
}

func (w *zapWrapper) Info(msg string, fields ...zap.Field) {
	w.z.Info(msg, fields...)
}

func (w *zapWrapper) Warn(msg string, fields ...zap.Field) {
	w.z.Warn(msg, fields...)
}

func (w *zapWrapper) Error(msg string, fields ...zap.Field) {
	w.z.Error(msg, fields...)
}

func (w *zapWrapper) Debugf(format string, args ...any) {
	w.s.Debugf(format, args...)
}

func (w *zapWrapper) Infof(format string, args ...any) {
	w.s.Infof(format, args...)
}

func (w *zapWrapper) Warnf(format string, args ...any) {
	w.s.Warnf(format, args...)
}

func (w *zapWrapper) Errorf(format string, args ...any) {
	w.s.Errorf(format, args...)
}

func (w *zapWrapper) Printf(format string, args ...any) {
	w.s.Infof(format, args...)
}

func (w *zapWrapper) With(fields ...zap.Field) Logger {
	return Wrap(w.z.With(fields...))
}

func (w *zapWrapper) Named(component string) Logger {
	return Wrap(w.z.Named(component))
}

func (w *zapWrapper) WithNode(nodeName string) Logger {
	return Wrap(w.z.With(zap.String("node", nodeName)))
}

func (w *zapWrapper) WithComponent(component string) Logger {
	return Wrap(w.z.With(zap.String("component", component)))
}

func (w *zapWrapper) WithPeer(peer string) Logger {
	return Wrap(w.z.With(zap.String("peer", peer)))
}

func (w *zapWrapper) Desugar() *zap.Logger {
	return w.z
}

func (w *zapWrapper) Sugar() *zap.SugaredLogger {
	return w.s
}

func (w *zapWrapper) ToStdLogger() *log.Logger {
	return zap.NewStdLog(w.z)
}

func (w *zapWrapper) Logf() Logf {
	return func(format string, args ...any) {
		w.s.Infof(format, args...)
	}
}

func (w *zapWrapper) Sync() error {
	return w.z.Sync()
}

// Wrap converts a *zap.Logger into a unified Logger interface.
func Wrap(z *zap.Logger) Logger {
	if z == nil {
		z = zap.NewNop()
	}
	return &zapWrapper{
		z: z,
		s: z.Sugar(),
	}
}

// NewNop returns a no-op Logger that discards all log output.
func NewNop() Logger {
	return Wrap(zap.NewNop())
}

// NewLogger initializes a unified Zap-backed Logger based on level and format.
func NewLogger(levelStr string, format string) (Logger, error) {
	z, err := New(levelStr, format)
	if err != nil {
		return nil, err
	}
	return Wrap(z), nil
}

// New initializes a raw *zap.Logger based on level and format.
func New(levelStr string, format string) (*zap.Logger, error) {
	if levelStr == "" {
		levelStr = os.Getenv("HERD_LOG_LEVEL")
	}
	if levelStr == "" {
		levelStr = "info"
	}

	var level zapcore.Level
	switch strings.ToLower(strings.TrimSpace(levelStr)) {
	case "debug":
		level = zapcore.DebugLevel
	case "info":
		level = zapcore.InfoLevel
	case "warn", "warning":
		level = zapcore.WarnLevel
	case "error":
		level = zapcore.ErrorLevel
	default:
		return nil, fmt.Errorf("invalid log level: %s", levelStr)
	}

	if format == "" {
		format = os.Getenv("HERD_LOG_FORMAT")
	}
	if format == "" {
		format = "console"
	}

	var encoder zapcore.Encoder
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	if strings.ToLower(format) == "json" {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	} else {
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	}

	core := zapcore.NewCore(encoder, zapcore.AddSync(os.Stderr), level)
	return zap.New(core), nil
}

// ZapToStdLogger adapts a Zap logger to a standard library *log.Logger for memberlist.
func ZapToStdLogger(z *zap.Logger) *log.Logger {
	return zap.NewStdLog(z)
}

// ZapToLogf adapts a Zap sugared logger to a Tailcat Logf signature func(format string, args ...any).
func ZapToLogf(z *zap.SugaredLogger) Logf {
	return func(format string, args ...any) {
		z.Infof(format, args...)
	}
}

// WithNode returns a logger annotated with the local node name.
func WithNode(z *zap.Logger, nodeName string) *zap.Logger {
	return z.With(zap.String("node", nodeName))
}

// WithPeer returns a logger annotated with a remote peer name or address.
func WithPeer(z *zap.Logger, peer string) *zap.Logger {
	return z.With(zap.String("peer", peer))
}

// WithComponent returns a logger annotated with a subsystem or component name.
func WithComponent(z *zap.Logger, component string) *zap.Logger {
	return z.With(zap.String("component", component))
}
