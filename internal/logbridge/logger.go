package logbridge

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

func parseLevel(s string) slog.Level {
	if s == "" {
		return slog.LevelInfo
	}
	normalized := strings.ToLower(s)
	if normalized == "warning" {
		normalized = "warn"
	}

	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(normalized)); err != nil {
		fmt.Fprintf(os.Stderr, "scrapd: unknown SCRAP_LOG_LEVEL %q, defaulting to INFO\n", s)
		return slog.LevelInfo
	}
	return lvl
}

// NewLogger creates a *slog.Logger with the given format and level.
// format: "json" (default) | "text" (logfmt)
// level: "debug" | "info" (default) | "warn" | "error"
func NewLogger(format, level string, w io.Writer) *slog.Logger {
	lvl := parseLevel(level)
	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	switch strings.ToLower(format) {
	case "text":
		handler = slog.NewTextHandler(w, opts)
	default:
		handler = slog.NewJSONHandler(w, opts)
	}

	return slog.New(handler)
}

// NewLoggerFromEnv creates a *slog.Logger reading SCRAP_LOG_FORMAT and
// SCRAP_LOG_LEVEL from environment variables.
func NewLoggerFromEnv(w io.Writer) *slog.Logger {
	format := os.Getenv("SCRAP_LOG_FORMAT")
	level := os.Getenv("SCRAP_LOG_LEVEL")
	return NewLogger(format, level, w)
}
