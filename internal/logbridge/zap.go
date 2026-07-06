package logbridge

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type slogCore struct {
	logger *slog.Logger
}

var _ zapcore.Core = (*slogCore)(nil)

func (c *slogCore) Enabled(level zapcore.Level) bool {
	return c.logger.Enabled(context.Background(), zapToSlogLevel(level))
}

func (c *slogCore) With(fields []zapcore.Field) zapcore.Core {
	args := make([]any, 0, len(fields)*2) //nolint:mnd // key+value per field
	for _, f := range fields {
		zapFieldAppend(&args, f)
	}
	return &slogCore{logger: c.logger.With(args...)}
}

func (c *slogCore) Check(entry zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return ce.AddCore(entry, c)
	}
	return ce
}

func (c *slogCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	attrs := make([]slog.Attr, 0, len(fields)+1)
	for _, f := range fields {
		attrs = append(attrs, zapFieldToAttr(f)...)
	}
	if entry.Stack != "" {
		attrs = append(attrs, slog.String("stacktrace", entry.Stack))
	}

	level := zapToSlogLevel(entry.Level)
	c.logger.LogAttrs(context.Background(), level, entry.Message, attrs...)
	return nil
}

func (c *slogCore) Sync() error {
	return nil
}

func zapToSlogLevel(level zapcore.Level) slog.Level {
	switch {
	case level <= zapcore.DebugLevel:
		return slog.LevelDebug
	case level == zapcore.InfoLevel:
		return slog.LevelInfo
	case level == zapcore.WarnLevel:
		return slog.LevelWarn
	default:
		return slog.LevelError
	}
}

func zapFieldAppend(args *[]any, f zapcore.Field) {
	switch f.Type {
	case zapcore.SkipType:
		return
	case zapcore.NamespaceType:
		return
	default:
		for _, a := range zapFieldToAttr(f) {
			*args = append(*args, a.Key, a.Value.Any())
		}
	}
}

//nolint:cyclop // flat type-switch mapping zap field types to slog attrs
func zapFieldToAttr(f zapcore.Field) []slog.Attr {
	switch f.Type {
	case zapcore.SkipType:
		return nil
	case zapcore.BoolType:
		return []slog.Attr{slog.Bool(f.Key, f.Integer == 1)}
	case zapcore.Int64Type, zapcore.Int32Type, zapcore.Int16Type, zapcore.Int8Type:
		return []slog.Attr{slog.Int64(f.Key, f.Integer)}
	case zapcore.Uint64Type, zapcore.Uint32Type, zapcore.Uint16Type, zapcore.Uint8Type, zapcore.UintptrType:
		return []slog.Attr{slog.Uint64(f.Key, uint64(f.Integer))} //nolint:gosec // zap stores unsigned values in int64 by design
	case zapcore.Float64Type:
		return []slog.Attr{slog.Float64(f.Key, math.Float64frombits(uint64(f.Integer)))} //nolint:gosec // zap stores float bits in int64 by design
	case zapcore.Float32Type:
		return []slog.Attr{slog.Float64(f.Key, float64(math.Float32frombits(uint32(f.Integer))))} //nolint:gosec // zap stores float32 bits in int64 by design
	case zapcore.StringType:
		return []slog.Attr{slog.String(f.Key, f.String)}
	case zapcore.ByteStringType:
		return []slog.Attr{slog.String(f.Key, string(f.Interface.([]byte)))}
	case zapcore.BinaryType:
		return []slog.Attr{slog.Any(f.Key, f.Interface)}
	case zapcore.DurationType:
		return []slog.Attr{slog.Duration(f.Key, time.Duration(f.Integer))}
	case zapcore.TimeType, zapcore.TimeFullType:
		return zapTimeToAttr(f)
	case zapcore.StringerType:
		return []slog.Attr{slog.String(f.Key, zapStringerString(f.Interface))}
	case zapcore.ErrorType:
		return []slog.Attr{slog.Any(f.Key, f.Interface)}
	case zapcore.NamespaceType:
		return nil
	case zapcore.Complex128Type:
		return []slog.Attr{slog.Any(f.Key, f.Interface)}
	case zapcore.Complex64Type:
		return []slog.Attr{slog.Any(f.Key, f.Interface)}
	case zapcore.ReflectType:
		return []slog.Attr{slog.Any(f.Key, f.Interface)}
	default:
		return zapFieldToAttrViaEncoder(f)
	}
}

func zapTimeToAttr(f zapcore.Field) []slog.Attr {
	// TimeFullType carries the value directly as a time.Time (used when the
	// timestamp does not fit the nanosecond int64 shape); TimeType carries the
	// nanos in f.Integer with an optional *time.Location in f.Interface.
	if t, ok := f.Interface.(time.Time); ok {
		return []slog.Attr{slog.Time(f.Key, t)}
	}
	if loc, ok := f.Interface.(*time.Location); ok {
		return []slog.Attr{slog.Time(f.Key, time.Unix(0, f.Integer).In(loc))}
	}
	return []slog.Attr{slog.Time(f.Key, time.Unix(0, f.Integer))}
}

// zapStringerString renders a StringerType field defensively: a nil interface,
// a non-Stringer payload, or a Stringer whose String() panics on a nil receiver
// must not take down the logging path.
func zapStringerString(v any) (s string) {
	stringer, ok := v.(fmt.Stringer)
	if !ok {
		return "<invalid stringer>"
	}
	defer func() {
		if recover() != nil {
			s = "<invalid stringer>"
		}
	}()
	return stringer.String()
}

func zapFieldToAttrViaEncoder(f zapcore.Field) []slog.Attr {
	enc := zapcore.NewMapObjectEncoder()
	f.AddTo(enc)
	attrs := make([]slog.Attr, 0, len(enc.Fields))
	for k, v := range enc.Fields {
		attrs = append(attrs, slog.Any(k, v))
	}
	return attrs
}

// NewZapLogger creates a *zap.Logger backed by the given slog.Logger.
func NewZapLogger(logger *slog.Logger) *zap.Logger {
	return zap.New(&slogCore{logger: logger}, zap.WithCaller(false))
}
