package metastore

import (
	"encoding/binary"
	"time"

	"github.com/petabytecl/scrap/internal/identity"
)

// PebbleKeySchemaV1 is the durable key-prefix version for metastore Pebble
// keys. V1 writes one leading schema byte before the logical key bytes so
// future key layouts can coexist during migrations.
const PebbleKeySchemaV1 byte = 0x01

const (
	maxKeyByte          = 0xff
	unixNanoSortSignBit = uint64(1) << 63
)

func pebbleKey(logical string) []byte {
	key := make([]byte, 0, 1+len(logical))
	key = append(key, PebbleKeySchemaV1)
	return append(key, []byte(logical)...)
}

func documentKey(doc identity.Document) []byte {
	return append(documentPrefix(), []byte(doc.TenantID+"\x00"+doc.TransactionID+"\x00"+doc.DocumentName)...)
}

func documentPrefix() []byte {
	return pebbleKey("document\x00")
}

func transactionPrefix() []byte {
	return pebbleKey("transaction\x00")
}

func transactionKey(transaction identity.Transaction) []byte {
	return append(transactionPrefix(), []byte(transaction.TenantID+"\x00"+transaction.TransactionID)...)
}

func transactionDocumentsRootPrefix() []byte {
	return pebbleKey("document_by_transaction\x00")
}

func transactionDocumentsPrefix(transaction identity.Transaction) []byte {
	return append(transactionDocumentsRootPrefix(), []byte(transaction.TenantID+"\x00"+transaction.TransactionID+"\x00")...)
}

func transactionDocumentKey(doc identity.Document) []byte {
	return append(transactionDocumentsPrefix(identity.Transaction{
		TenantID:      doc.TenantID,
		TransactionID: doc.TransactionID,
	}), []byte(doc.DocumentName)...)
}

func blockDocumentsRootPrefix() []byte {
	return pebbleKey("document_by_block\x00")
}

func blockDocumentsPrefix(blockID string) []byte {
	return append(blockDocumentsRootPrefix(), []byte(blockID+"\x00")...)
}

func blockDocumentKey(blockID string, doc identity.Document) []byte {
	return append(blockDocumentsPrefix(blockID), []byte(doc.TenantID+"\x00"+doc.TransactionID+"\x00"+doc.DocumentName)...)
}

func uploadIntentPrefix() []byte {
	return pebbleKey("upload_intent\x00")
}

func uploadIntentKey(blockID string) []byte {
	return append(uploadIntentPrefix(), []byte(blockID)...)
}

func processableUploadIntentPrefix() []byte {
	return pebbleKey("upload_intent_processable\x00")
}

func processableUploadIntentKey(intent UploadIntent) []byte {
	prefix := processableUploadIntentPrefix()
	key := make([]byte, 0, len(prefix)+len(intent.BlockID)+9)
	key = append(key, prefix...)
	key = binary.BigEndian.AppendUint64(key, uploadIntentUpdatedAtSortKey(intent.UpdatedAt))
	key = append(key, 0)
	return append(key, []byte(intent.BlockID)...)
}

func uploadIntentUpdatedAtSortKey(updatedAt time.Time) uint64 {
	return uint64(updatedAt.UTC().UnixNano()) ^ unixNanoSortSignBit
}

func repairStatesPrefix() []byte {
	return pebbleKey("repair_state\x00")
}

func repairStatePrefix(doc identity.Document) []byte {
	return append(repairStatesPrefix(), []byte(doc.TenantID+"\x00"+doc.TransactionID+"\x00"+doc.DocumentName+"\x00")...)
}

func repairStateKey(doc identity.Document, incidentID string) []byte {
	return append(repairStatePrefix(doc), []byte(incidentID)...)
}

func commandReceiptPrefix() []byte {
	return pebbleKey("command_receipt\x00")
}

func commandReceiptKey(commandID string) []byte {
	return append(commandReceiptPrefix(), []byte(commandID)...)
}

func prefixUpperBound(prefix []byte) []byte {
	out := append([]byte(nil), prefix...)
	for i := len(out) - 1; i >= 0; i-- {
		if out[i] != maxKeyByte {
			out[i]++
			return out[:i+1]
		}
	}
	return nil
}
