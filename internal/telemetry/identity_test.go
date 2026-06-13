package telemetry_test

import (
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"

	"github.com/petabytecl/scrap/internal/telemetry"
)

func TestDocumentIdentityAttributesHashRawIdentifiersByDefault(t *testing.T) {
	attrs := attrMap(telemetry.DocumentIdentityAttributes("tx-123", "invoice.xml", telemetry.HashIdentifiers))

	assertNotPresent(t, attrs, "scrap.transaction_id")
	assertNotPresent(t, attrs, "scrap.document_name")
	assertAttrNotContaining(t, attrs, "scrap.transaction.hash", "tx-123")
	assertAttrNotContaining(t, attrs, "scrap.document.hash", "invoice.xml")

	again := attrMap(telemetry.DocumentIdentityAttributes("tx-123", "invoice.xml", telemetry.HashIdentifiers))
	assertAttr(t, again, "scrap.transaction.hash", attrs[attribute.Key("scrap.transaction.hash")].AsString())
	assertAttr(t, again, "scrap.document.hash", attrs[attribute.Key("scrap.document.hash")].AsString())
}

func TestDocumentIdentityAttributesExposeRawIdentifiersOnlyForLocalDebug(t *testing.T) {
	attrs := attrMap(telemetry.DocumentIdentityAttributes("tx-123", "invoice.xml", telemetry.RawIdentifiersForLocalDebug))

	assertAttr(t, attrs, "scrap.transaction_id", "tx-123")
	assertAttr(t, attrs, "scrap.document_name", "invoice.xml")
	assertNotPresent(t, attrs, "scrap.transaction.hash")
	assertNotPresent(t, attrs, "scrap.document.hash")
}

func TestResolveIdentifierModeIsFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		cellID string
		raw    bool
		want   telemetry.IdentifierMode
	}{
		{"local cell with raw requested exposes raw", "local", true, telemetry.RawIdentifiersForLocalDebug},
		{"local cell without raw request stays hashed", "local", false, telemetry.HashIdentifiers},
		{"production cell refuses raw request", "kind-dev", true, telemetry.HashIdentifiers},
		{"stress cell refuses raw request", "kind-stress", true, telemetry.HashIdentifiers},
		{"empty cell is not local", "", true, telemetry.HashIdentifiers},
		{"cell id match is case-sensitive", "Local", true, telemetry.HashIdentifiers},
		{"production cell without raw request stays hashed", "kind-dev", false, telemetry.HashIdentifiers},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := telemetry.ResolveIdentifierMode(tt.cellID, tt.raw); got != tt.want {
				t.Fatalf("ResolveIdentifierMode(%q, %v) = %v, want %v", tt.cellID, tt.raw, got, tt.want)
			}
		})
	}
}

func assertNotPresent(t *testing.T, attrs map[attribute.Key]attribute.Value, key string) {
	t.Helper()

	if _, ok := attrs[attribute.Key(key)]; ok {
		t.Fatalf("attribute %q is present", key)
	}
}

func assertAttrNotContaining(t *testing.T, attrs map[attribute.Key]attribute.Value, key, forbidden string) {
	t.Helper()

	got, ok := attrs[attribute.Key(key)]
	if !ok {
		t.Fatalf("missing attribute %q", key)
	}
	if got.AsString() == "" {
		t.Fatalf("attribute %q is empty", key)
	}
	if strings.Contains(got.AsString(), forbidden) {
		t.Fatalf("attribute %q exposes raw identifier %q", key, forbidden)
	}
}
