package metastore

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/testutil"
)

type pebbleKeyFixture struct {
	name string
	key  []byte
}

func TestPebbleKeySchemaV1Fixture(t *testing.T) {
	fixtures := pebbleKeySchemaV1Fixtures()
	for _, fixture := range fixtures {
		testutil.RequireTruef(t, len(fixture.key) > 0, "%s key is empty", fixture.name)
		testutil.RequireEqualf(t, PebbleKeySchemaV1, fixture.key[0], "%s key schema version", fixture.name)
	}

	expected := renderPebbleKeySchemaFixture(fixtures)
	path := filepath.Join("testdata", "pebble_key_schema_v1.txt")
	cleanPath := filepath.Clean(path)
	if os.Getenv("UPDATE_PEBBLE_KEY_SCHEMA_FIXTURE") == "1" {
		testutil.RequireNoErrorf(t, os.MkdirAll(filepath.Dir(cleanPath), 0o750), "create fixture directory")
		testutil.RequireNoErrorf(t, os.WriteFile(cleanPath, expected, 0o600), "write fixture")
	}
	data, err := os.ReadFile(cleanPath)
	testutil.RequireNoErrorf(t, err, "read fixture")
	if !bytes.Equal(data, expected) {
		t.Fatalf("Pebble key schema V1 fixture drifted; run UPDATE_PEBBLE_KEY_SCHEMA_FIXTURE=1 go test ./internal/metastore to refresh intentionally")
	}
}

func pebbleKeySchemaV1Fixtures() []pebbleKeyFixture {
	doc := identity.Document{
		TenantID:      "tenant-a",
		TransactionID: "txn-001",
		DocumentName:  "invoice.xml",
	}
	transaction := identity.Transaction{
		TenantID:      doc.TenantID,
		TransactionID: doc.TransactionID,
	}
	intent := UploadIntent{
		BlockID:   "block-001",
		UpdatedAt: time.Unix(1_700_000_000, 123_456_789).UTC(),
	}

	return []pebbleKeyFixture{
		{name: "document_prefix", key: documentPrefix()},
		{name: "document", key: documentKey(doc)},
		{name: "transaction_prefix", key: transactionPrefix()},
		{name: "transaction", key: transactionKey(transaction)},
		{name: "transaction_documents_root_prefix", key: transactionDocumentsRootPrefix()},
		{name: "transaction_documents_prefix", key: transactionDocumentsPrefix(transaction)},
		{name: "transaction_document", key: transactionDocumentKey(doc)},
		{name: "block_documents_root_prefix", key: blockDocumentsRootPrefix()},
		{name: "block_documents_prefix", key: blockDocumentsPrefix(intent.BlockID)},
		{name: "block_document", key: blockDocumentKey(intent.BlockID, doc)},
		{name: "upload_intent_prefix", key: uploadIntentPrefix()},
		{name: "upload_intent", key: uploadIntentKey(intent.BlockID)},
		{name: "processable_upload_intent_prefix", key: processableUploadIntentPrefix()},
		{name: "processable_upload_intent", key: processableUploadIntentKey(intent)},
		{name: "repair_states_prefix", key: repairStatesPrefix()},
		{name: "repair_state_prefix", key: repairStatePrefix(doc)},
		{name: "repair_state", key: repairStateKey(doc, "incident-001")},
		{name: "command_receipt_prefix", key: commandReceiptPrefix()},
		{name: "command_receipt", key: commandReceiptKey("cmd-001")},
	}
}

func renderPebbleKeySchemaFixture(fixtures []pebbleKeyFixture) []byte {
	var builder strings.Builder
	for _, fixture := range fixtures {
		builder.WriteString(fixture.name)
		builder.WriteString(": ")
		builder.WriteString(hex.EncodeToString(fixture.key))
		builder.WriteByte('\n')
	}
	return []byte(builder.String())
}
