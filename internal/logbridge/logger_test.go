package logbridge_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/logbridge"
)

func TestNewLogger_JSONFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := logbridge.NewLogger("json", "info", &buf)

	logger.Info("hello", "key", "value")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %q", err, buf.String())
	}
	if got := entry["msg"]; got != "hello" {
		t.Errorf("msg: got %q, want %q", got, "hello")
	}
	if got := entry["level"]; got != "INFO" {
		t.Errorf("level: got %q, want %q", got, "INFO")
	}
	if got := entry["key"]; got != "value" {
		t.Errorf("key: got %q, want %q", got, "value")
	}
}

func TestNewLogger_TextFormat_Logfmt(t *testing.T) {
	var buf bytes.Buffer
	logger := logbridge.NewLogger("text", "info", &buf)

	logger.Info("sealed block", "component", "shard", "block_id", 42)

	line := buf.String()
	// logfmt: key=value pairs, space-separated
	if !strings.Contains(line, "level=INFO") {
		t.Errorf("missing level=INFO in logfmt output: %q", line)
	}
	if !strings.Contains(line, "msg=\"sealed block\"") {
		t.Errorf("missing msg=\"sealed block\" in logfmt output: %q", line)
	}
	if !strings.Contains(line, "component=shard") {
		t.Errorf("missing component=shard in logfmt output: %q", line)
	}
	if !strings.Contains(line, "block_id=42") {
		t.Errorf("missing block_id=42 in logfmt output: %q", line)
	}
}

func TestNewLogger_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger := logbridge.NewLogger("json", "warn", &buf)

	logger.Info("should be hidden")
	if buf.Len() != 0 {
		t.Errorf("INFO should be filtered at WARN level, got: %q", buf.String())
	}

	logger.Warn("should appear")
	if buf.Len() == 0 {
		t.Error("WARN should not be filtered at WARN level")
	}
}

func TestNewLogger_Defaults(t *testing.T) {
	var buf bytes.Buffer
	logger := logbridge.NewLogger("", "", &buf)

	logger.Info("test")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("default format should be JSON: %v\nraw: %q", err, buf.String())
	}
}

func TestNewLogger_DebugLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := logbridge.NewLogger("json", "debug", &buf)

	logger.Debug("verbose")
	if buf.Len() == 0 {
		t.Error("DEBUG should be visible at debug level")
	}
}

func TestNewLogger_ComponentAttr(t *testing.T) {
	var buf bytes.Buffer
	logger := logbridge.NewLogger("json", "info", &buf)

	child := logger.With("component", "raft")
	child.Info("election won")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if got := entry["component"]; got != "raft" {
		t.Errorf("component: got %q, want %q", got, "raft")
	}
}

func TestNewLoggerFromEnv(t *testing.T) {
	t.Setenv("SCRAP_LOG_FORMAT", "text")
	t.Setenv("SCRAP_LOG_LEVEL", "error")

	var buf bytes.Buffer
	logger := logbridge.NewLoggerFromEnv(&buf)

	logger.Warn("should be filtered")
	if buf.Len() != 0 {
		t.Errorf("WARN should be filtered at ERROR level, got: %q", buf.String())
	}

	logger.Error("should appear")
	if buf.Len() == 0 {
		t.Error("ERROR should not be filtered at ERROR level")
	}
	if !strings.Contains(buf.String(), "level=ERROR") {
		t.Errorf("expected logfmt text output, got: %q", buf.String())
	}
}

func TestNewLoggerFromEnv_Defaults(t *testing.T) {
	t.Setenv("SCRAP_LOG_FORMAT", "")
	t.Setenv("SCRAP_LOG_LEVEL", "")

	var buf bytes.Buffer
	logger := logbridge.NewLoggerFromEnv(&buf)
	logger.Info("test")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("default should be JSON format: %v\nraw: %q", err, buf.String())
	}

	// Verify INFO is visible by default
	if entry["msg"] != "test" {
		t.Errorf("msg: got %q, want %q", entry["msg"], "test")
	}
}
