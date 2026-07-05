package logbridge

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"go.etcd.io/raft/v3"
)

var _ raft.Logger = (*raftLogger)(nil)

type raftLogger struct {
	log *slog.Logger
}

// NewRaftLogger creates a raft.Logger that delegates to the given slog.Logger.
// A nil logger falls back to slog.Default() so a misconfigured caller cannot
// turn the first raft log line into a nil-pointer panic.
func NewRaftLogger(logger *slog.Logger) raft.Logger {
	if logger == nil {
		logger = slog.Default()
	}
	return &raftLogger{log: logger}
}

func (r *raftLogger) enabled(level slog.Level) bool {
	return r.log.Enabled(context.Background(), level)
}

func (r *raftLogger) Debug(v ...any) {
	if !r.enabled(slog.LevelDebug) {
		return
	}
	r.log.Debug(fmt.Sprint(v...))
}

func (r *raftLogger) Debugf(format string, v ...any) {
	if !r.enabled(slog.LevelDebug) {
		return
	}
	r.log.Debug(fmt.Sprintf(format, v...))
}

func (r *raftLogger) Info(v ...any) {
	if !r.enabled(slog.LevelInfo) {
		return
	}
	r.log.Info(fmt.Sprint(v...))
}

func (r *raftLogger) Infof(format string, v ...any) {
	if !r.enabled(slog.LevelInfo) {
		return
	}
	r.log.Info(fmt.Sprintf(format, v...))
}

func (r *raftLogger) Warning(v ...any) {
	if !r.enabled(slog.LevelWarn) {
		return
	}
	r.log.Warn(fmt.Sprint(v...))
}

func (r *raftLogger) Warningf(format string, v ...any) {
	if !r.enabled(slog.LevelWarn) {
		return
	}
	r.log.Warn(fmt.Sprintf(format, v...))
}

func (r *raftLogger) Error(v ...any) {
	if !r.enabled(slog.LevelError) {
		return
	}
	r.log.Error(fmt.Sprint(v...))
}

func (r *raftLogger) Errorf(format string, v ...any) {
	if !r.enabled(slog.LevelError) {
		return
	}
	r.log.Error(fmt.Sprintf(format, v...))
}

func (r *raftLogger) Fatal(v ...any) {
	msg := fmt.Sprint(v...)
	r.log.Error(msg, "raft_level", "FATAL")
	os.Exit(1)
}

func (r *raftLogger) Fatalf(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	r.log.Error(msg, "raft_level", "FATAL")
	os.Exit(1)
}

func (r *raftLogger) Panic(v ...any) {
	msg := fmt.Sprint(v...)
	r.log.Error(msg, "raft_level", "PANIC")
	panic(msg)
}

func (r *raftLogger) Panicf(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	r.log.Error(msg, "raft_level", "PANIC")
	panic(msg)
}
