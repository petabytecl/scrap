package audit

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestPolicySinkRejectsOversizedEvents(t *testing.T) {
	event, err := NewEvent(EventInput{
		PrincipalID: "principal",
		Role:        "admin_operator",
		Surface:     SurfaceAdmin,
		Operation:   OperationEvictionApply,
		Target:      TargetBlock,
		Result:      ResultAllowed,
		Reason:      ReasonAllowed,
		Now:         time.Unix(10, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}

	// Bypass NewPolicySink so the budget can sit below the policy floor;
	// every well-formed event is bigger than 8 bytes.
	sink := &PolicySink{
		inner:  NewMemorySink(),
		policy: Policy{Sink: "log", FailureMode: FailureModeFailClosed, MaxEventBytes: 8},
		logger: slog.New(slog.DiscardHandler),
	}
	err = sink.Record(context.Background(), event)
	if err == nil {
		t.Fatal("Record accepted an oversized event, want error")
	}
	if !strings.Contains(err.Error(), "exceeds policy max") {
		t.Fatalf("Record error = %v, want size rejection", err)
	}
}
