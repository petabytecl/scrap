package logbridge_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/petabytecl/scrap/internal/logbridge"
)

func parseJSONLog(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %q", err, buf.String())
	}
	return entry
}

func TestRaftLogger_Info(t *testing.T) {
	var buf bytes.Buffer
	logger := logbridge.NewLogger("json", "debug", &buf)
	rl := logbridge.NewRaftLogger(logger)

	rl.Info("node started")

	entry := parseJSONLog(t, &buf)
	if got := entry["level"]; got != "INFO" {
		t.Errorf("level: got %q, want INFO", got)
	}
	if got, ok := entry["msg"].(string); !ok || got != "node started" {
		t.Errorf("msg: got %q, want %q", got, "node started")
	}
}

func TestRaftLogger_Infof(t *testing.T) {
	var buf bytes.Buffer
	logger := logbridge.NewLogger("json", "debug", &buf)
	rl := logbridge.NewRaftLogger(logger)

	rl.Infof("node %d started at term %d", 1, 5)

	entry := parseJSONLog(t, &buf)
	if got := entry["msg"]; got != "node 1 started at term 5" {
		t.Errorf("msg: got %q, want %q", got, "node 1 started at term 5")
	}
}

func TestRaftLogger_Warning(t *testing.T) {
	var buf bytes.Buffer
	logger := logbridge.NewLogger("json", "debug", &buf)
	rl := logbridge.NewRaftLogger(logger)

	rl.Warning("leader lost")

	entry := parseJSONLog(t, &buf)
	if got := entry["level"]; got != "WARN" {
		t.Errorf("level: got %q, want WARN", got)
	}
}

func TestRaftLogger_Warningf(t *testing.T) {
	var buf bytes.Buffer
	logger := logbridge.NewLogger("json", "debug", &buf)
	rl := logbridge.NewRaftLogger(logger)

	rl.Warningf("peer %d unreachable", 3)

	entry := parseJSONLog(t, &buf)
	if got := entry["msg"]; got != "peer 3 unreachable" {
		t.Errorf("msg: got %q, want %q", got, "peer 3 unreachable")
	}
}

func TestRaftLogger_Debug(t *testing.T) {
	var buf bytes.Buffer
	logger := logbridge.NewLogger("json", "debug", &buf)
	rl := logbridge.NewRaftLogger(logger)

	rl.Debug("heartbeat sent")

	entry := parseJSONLog(t, &buf)
	if got := entry["level"]; got != "DEBUG" {
		t.Errorf("level: got %q, want DEBUG", got)
	}
}

func TestRaftLogger_Debugf(t *testing.T) {
	var buf bytes.Buffer
	logger := logbridge.NewLogger("json", "debug", &buf)
	rl := logbridge.NewRaftLogger(logger)

	rl.Debugf("sending %d entries", 42)

	entry := parseJSONLog(t, &buf)
	if got := entry["msg"]; got != "sending 42 entries" {
		t.Errorf("msg: got %q, want %q", got, "sending 42 entries")
	}
}

func TestRaftLogger_Error(t *testing.T) {
	var buf bytes.Buffer
	logger := logbridge.NewLogger("json", "debug", &buf)
	rl := logbridge.NewRaftLogger(logger)

	rl.Error("wal write failed")

	entry := parseJSONLog(t, &buf)
	if got := entry["level"]; got != "ERROR" {
		t.Errorf("level: got %q, want ERROR", got)
	}
}

func TestRaftLogger_Errorf(t *testing.T) {
	var buf bytes.Buffer
	logger := logbridge.NewLogger("json", "debug", &buf)
	rl := logbridge.NewRaftLogger(logger)

	rl.Errorf("wal write failed: %v", "disk full")

	entry := parseJSONLog(t, &buf)
	if got := entry["msg"]; got != "wal write failed: disk full" {
		t.Errorf("msg: got %q, want %q", got, "wal write failed: disk full")
	}
}

func TestRaftLogger_Panic(t *testing.T) {
	var buf bytes.Buffer
	logger := logbridge.NewLogger("json", "debug", &buf)
	rl := logbridge.NewRaftLogger(logger)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Panic should panic")
		}
		if got, ok := r.(string); !ok || got != "invariant violated" {
			t.Errorf("panic value: got %q, want %q", r, "invariant violated")
		}
		entry := parseJSONLog(t, &buf)
		if got := entry["level"]; got != "ERROR" {
			t.Errorf("level: got %q, want ERROR", got)
		}
		if got := entry["raft_level"]; got != "PANIC" {
			t.Errorf("raft_level: got %q, want PANIC", got)
		}
	}()

	rl.Panic("invariant violated")
}

func TestRaftLogger_Panicf(t *testing.T) {
	var buf bytes.Buffer
	logger := logbridge.NewLogger("json", "debug", &buf)
	rl := logbridge.NewRaftLogger(logger)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Panicf should panic")
		}
		if got, ok := r.(string); !ok || got != "index 5 > last 3" {
			t.Errorf("panic value: got %q, want %q", r, "index 5 > last 3")
		}
	}()

	rl.Panicf("index %d > last %d", 5, 3)
}

func TestRaftLogger_DebugFilteredAtInfoLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := logbridge.NewLogger("json", "info", &buf)
	rl := logbridge.NewRaftLogger(logger)

	rl.Debug("should be hidden")
	if buf.Len() != 0 {
		t.Errorf("DEBUG should be filtered at INFO level, got: %q", buf.String())
	}
}
