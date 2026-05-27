package logbridge_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"go.uber.org/zap"

	"github.com/petabytecl/scrap/internal/logbridge"
)

func TestZapLogger_InfoDelegatesToSlog(t *testing.T) {
	var buf bytes.Buffer
	slogger := logbridge.NewLogger("json", "debug", &buf)
	zapLogger := logbridge.NewZapLogger(slogger)

	zapLogger.Info("wal opened")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %q", err, buf.String())
	}
	if got := entry["level"]; got != "INFO" {
		t.Errorf("level: got %q, want INFO", got)
	}
	if got := entry["msg"]; got != "wal opened" {
		t.Errorf("msg: got %q, want %q", got, "wal opened")
	}
}

func TestZapLogger_ErrorDelegatesToSlog(t *testing.T) {
	var buf bytes.Buffer
	slogger := logbridge.NewLogger("json", "debug", &buf)
	zapLogger := logbridge.NewZapLogger(slogger)

	zapLogger.Error("snap write failed")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %q", err, buf.String())
	}
	if got := entry["level"]; got != "ERROR" {
		t.Errorf("level: got %q, want ERROR", got)
	}
}

func TestZapLogger_DebugDelegatesToSlog(t *testing.T) {
	var buf bytes.Buffer
	slogger := logbridge.NewLogger("json", "debug", &buf)
	zapLogger := logbridge.NewZapLogger(slogger)

	zapLogger.Debug("reading segment")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %q", err, buf.String())
	}
	if got := entry["level"]; got != "DEBUG" {
		t.Errorf("level: got %q, want DEBUG", got)
	}
}

func TestZapLogger_WarnDelegatesToSlog(t *testing.T) {
	var buf bytes.Buffer
	slogger := logbridge.NewLogger("json", "debug", &buf)
	zapLogger := logbridge.NewZapLogger(slogger)

	zapLogger.Warn("slow fsync")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %q", err, buf.String())
	}
	if got := entry["level"]; got != "WARN" {
		t.Errorf("level: got %q, want WARN", got)
	}
}

func TestZapLogger_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	slogger := logbridge.NewLogger("json", "warn", &buf)
	zapLogger := logbridge.NewZapLogger(slogger)

	zapLogger.Info("should be hidden")
	if buf.Len() != 0 {
		t.Errorf("INFO should be filtered at WARN level, got: %q", buf.String())
	}

	zapLogger.Warn("should appear")
	if buf.Len() == 0 {
		t.Error("WARN should not be filtered at WARN level")
	}
}

func TestZapLogger_WithFields(t *testing.T) {
	var buf bytes.Buffer
	slogger := logbridge.NewLogger("json", "debug", &buf)
	zapLogger := logbridge.NewZapLogger(slogger)

	zapLogger.With(zap.String("dir", "/data/wal")).Info("segment opened")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %q", err, buf.String())
	}
	if got := entry["dir"]; got != "/data/wal" {
		t.Errorf("dir: got %q, want %q", got, "/data/wal")
	}
}
