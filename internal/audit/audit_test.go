package audit_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/audit"
)

func TestNewEventBoundsPrincipalAndLabels(t *testing.T) {
	rawPrincipal := "spiffe://scrap/cell/cell-a/member/scrapd-0/member-a"
	event, err := audit.NewEvent(audit.EventInput{
		PrincipalID: rawPrincipal,
		Role:        "admin_operator",
		Surface:     audit.SurfaceAdmin,
		Operation:   "eviction_apply",
		Target:      audit.TargetBlock,
		Result:      audit.ResultAllowed,
		Reason:      audit.ReasonAllowed,
	})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	if event.Principal == "" || event.Principal == rawPrincipal {
		t.Fatalf("principal = %q, want bounded handle", event.Principal)
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if strings.Contains(string(data), rawPrincipal) {
		t.Fatalf("event leaked raw principal: %s", string(data))
	}
	for _, want := range []string{"admin_operator", "admin", "eviction_apply", "block", "allowed", "ok"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("event missing %q: %s", want, string(data))
		}
	}
}

func TestNewEventRejectsHighCardinalityLabels(t *testing.T) {
	_, err := audit.NewEvent(audit.EventInput{
		PrincipalID: "principal",
		Role:        "admin_operator",
		Surface:     audit.SurfaceAdmin,
		Operation:   "eviction_apply_plan_123",
		Target:      audit.TargetBlock,
		Result:      audit.ResultDenied,
		Reason:      audit.ReasonPermissionDenied,
	})
	if err == nil {
		t.Fatal("NewEvent succeeded with high-cardinality operation, want error")
	}
}

func TestNewEventAcceptsEvidenceHookOperations(t *testing.T) {
	for _, operation := range []string{
		audit.OperationProjectionKeyHook,
		audit.OperationTransitRotateHook,
		audit.OperationLightScrubHook,
	} {
		t.Run(operation, func(t *testing.T) {
			_, err := audit.NewEvent(audit.EventInput{
				PrincipalID: "principal",
				Role:        "admin_break_glass",
				Surface:     audit.SurfaceAdmin,
				Operation:   operation,
				Target:      audit.TargetEvidence,
				Result:      audit.ResultAllowed,
				Reason:      audit.ReasonAllowed,
			})
			if err != nil {
				t.Fatalf("NewEvent: %v", err)
			}
		})
	}
}

func TestMemorySinkStoresImmutableEvents(t *testing.T) {
	sink := audit.NewMemorySink()
	event, err := audit.NewEvent(audit.EventInput{
		PrincipalID: "principal",
		Role:        "document_reader",
		Surface:     audit.SurfacePublic,
		Operation:   "read_document",
		Target:      audit.TargetDocument,
		Result:      audit.ResultDenied,
		Reason:      audit.ReasonMissingRole,
	})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	if err := sink.Record(context.Background(), event); err != nil {
		t.Fatalf("Record: %v", err)
	}
	events := sink.Events()
	events[0].Reason = "mutated"
	if got := sink.Events()[0].Reason; got != audit.ReasonMissingRole {
		t.Fatalf("stored event reason = %q, want %q", got, audit.ReasonMissingRole)
	}
}

func TestLoadPolicyValidatesBoundedShape(t *testing.T) {
	policy, err := audit.LoadPolicy(writeAuditJSONFixture(t, map[string]any{
		"sink":            "stderr",
		"failure_mode":    "fail_closed",
		"max_event_bytes": 512,
	}))
	if err != nil {
		t.Fatalf("LoadPolicy valid: %v", err)
	}
	if policy.Sink != "stderr" || policy.FailureMode != "fail_closed" || policy.MaxEventBytes != 512 {
		t.Fatalf("policy = %+v", policy)
	}

	for name, body := range map[string]any{
		"missing sink": map[string]any{"failure_mode": "fail_closed"},
		"bad mode":     map[string]any{"sink": "stderr", "failure_mode": "ignore"},
		"too large":    map[string]any{"sink": "stderr", "max_event_bytes": 8192},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := audit.LoadPolicy(writeAuditJSONFixture(t, body)); err == nil {
				t.Fatal("LoadPolicy succeeded, want error")
			}
		})
	}
}

func TestLoggerAndNopSinksAcceptValidEvents(t *testing.T) {
	event, err := audit.NewEvent(audit.EventInput{
		PrincipalID: "principal",
		Role:        "peer_member",
		Surface:     audit.SurfacePeer,
		Operation:   audit.OperationTransferBlock,
		Target:      audit.TargetBlock,
		Result:      audit.ResultAllowed,
		Reason:      audit.ReasonAllowed,
	})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	if err := audit.NewLoggerSink(slog.New(slog.DiscardHandler)).Record(context.Background(), event); err != nil {
		t.Fatalf("logger sink record: %v", err)
	}
	if err := audit.NewNopSink().Record(context.Background(), event); err != nil {
		t.Fatalf("nop sink record: %v", err)
	}
}

func TestLoggerSinkReturnsHandlerErrors(t *testing.T) {
	event, err := audit.NewEvent(audit.EventInput{
		PrincipalID: "principal",
		Role:        "admin_operator",
		Surface:     audit.SurfaceAdmin,
		Operation:   audit.OperationEvictionApply,
		Target:      audit.TargetBlock,
		Result:      audit.ResultAllowed,
		Reason:      audit.ReasonAllowed,
	})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	wantErr := errors.New("write audit")
	sink := audit.NewLoggerSink(slog.New(errorHandler{err: wantErr}))
	if err := sink.Record(context.Background(), event); !errors.Is(err, wantErr) {
		t.Fatalf("Record = %v, want handler error", err)
	}
}

type errorHandler struct {
	err error
}

func (h errorHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (h errorHandler) Handle(context.Context, slog.Record) error {
	return h.err
}

func (h errorHandler) WithAttrs([]slog.Attr) slog.Handler {
	return h
}

func (h errorHandler) WithGroup(string) slog.Handler {
	return h
}

func writeAuditJSONFixture(t *testing.T, value any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.json")
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}
