package telemetry

import (
	"crypto/sha256"
	"encoding/hex"

	"go.opentelemetry.io/otel/attribute"
)

const documentIdentityAttributeCount = 2

// LocalCellID is the reserved cell_id that designates local non-production mode
// (see CONTEXT.md, "Member Identity Model"). It is the only Cell in which raw
// identifier telemetry may be emitted.
const LocalCellID = "local"

// IdentifierMode controls how document identifiers are attached to trace/log
// telemetry attributes.
type IdentifierMode int

const (
	// HashIdentifiers emits stable hashes and does not expose raw identifiers.
	// It is the zero value, so any caller that forgets to set a mode is
	// fail-closed to hashed identifiers.
	HashIdentifiers IdentifierMode = iota
	// RawIdentifiersForLocalDebug emits raw identifiers for explicit local
	// debugging only.
	RawIdentifiersForLocalDebug
)

// ResolveIdentifierMode decides how Document identifiers are attached to
// telemetry. It is fail-closed (ADR 0013 §4): raw identifiers are emitted only
// when they are explicitly requested, AND the Cell is the reserved local
// non-production Cell. Every other combination — most importantly a raw request
// in a production Cell — resolves to HashIdentifiers.
func ResolveIdentifierMode(cellID string, rawIDsRequested bool) IdentifierMode {
	if rawIDsRequested && cellID == LocalCellID {
		return RawIdentifiersForLocalDebug
	}
	return HashIdentifiers
}

// DocumentIdentityAttributes returns trace/log attributes for a Document
// identity using the requested identifier mode.
func DocumentIdentityAttributes(txID, docName string, mode IdentifierMode) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, documentIdentityAttributeCount)
	if mode == RawIdentifiersForLocalDebug {
		if txID != "" {
			attrs = append(attrs, attribute.String("scrap.transaction_id", txID))
		}
		if docName != "" {
			attrs = append(attrs, attribute.String("scrap.document_name", docName))
		}
		return attrs
	}

	if txID != "" {
		attrs = append(attrs, attribute.String("scrap.transaction.hash", hashIdentifier("transaction", txID)))
	}
	if docName != "" {
		attrs = append(attrs, attribute.String("scrap.document.hash", hashIdentifier("document", docName)))
	}
	return attrs
}

func hashIdentifier(namespace, value string) string {
	sum := sha256.Sum256([]byte(namespace + "\x00" + value))
	return hex.EncodeToString(sum[:])
}
