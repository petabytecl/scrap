package telemetry

import (
	"crypto/sha256"
	"encoding/hex"

	"go.opentelemetry.io/otel/attribute"
)

const documentIdentityAttributeCount = 2

// IdentifierMode controls how document identifiers are attached to trace/log
// telemetry attributes.
type IdentifierMode int

const (
	// HashIdentifiers emits stable hashes and does not expose raw identifiers.
	HashIdentifiers IdentifierMode = iota
	// RawIdentifiersForLocalDebug emits raw identifiers for explicit local
	// debugging only.
	RawIdentifiersForLocalDebug
)

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
