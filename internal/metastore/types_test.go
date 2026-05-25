package metastore

import (
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestTransactionAppendDocumentReturnsUpdatedCopy(t *testing.T) {
	transaction := Transaction{
		Identity: identity.Transaction{
			TenantID:      "tenant",
			TransactionID: "txn",
		},
		State: TransactionStateOpen,
	}

	updated, err := transaction.AppendDocument(DocumentClassPermanent)
	testutil.RequireNoErrorf(t, err, "append permanent document")
	requireTransactionCounts(t, transaction, 0, 0, 0)
	requireTransactionCounts(t, updated, 1, 1, 0)

	updated, err = updated.AppendDocument(DocumentClassEphemeral)
	testutil.RequireNoErrorf(t, err, "append ephemeral document")
	requireTransactionCounts(t, updated, 2, 1, 1)
}

func TestTransactionAppendDocumentRejectsClosedTransaction(t *testing.T) {
	transaction := Transaction{
		Identity: identity.Transaction{
			TenantID:      "tenant",
			TransactionID: "txn",
		},
		State: TransactionStateCompleted,
	}

	_, err := transaction.AppendDocument(DocumentClassPermanent)
	testutil.RequireErrorIsf(t, err, ErrTransactionClosed, "append closed transaction")
}

func TestTransactionCompleteReturnsUpdatedCopy(t *testing.T) {
	completedAt := time.Unix(100, 0).UTC()
	tags := map[string]string{"owner": "billing"}
	transaction := Transaction{
		Identity: identity.Transaction{
			TenantID:      "tenant",
			TransactionID: "txn",
		},
		State: TransactionStateOpen,
	}

	completed, changed, err := transaction.Complete(completedAt, tags)
	testutil.RequireNoErrorf(t, err, "complete transaction")
	testutil.RequireTruef(t, changed, "complete transaction changed")
	testutil.RequireEqualf(t, transaction.State, TransactionStateOpen, "original transaction state")
	testutil.RequireNilf(t, transaction.CompletedAt, "original completed_at")
	testutil.RequireEqualf(t, completed.State, TransactionStateCompleted, "completed transaction state")
	testutil.RequireNotNilf(t, completed.CompletedAt, "completed_at")
	testutil.RequireTruef(t, completed.CompletedAt.Equal(completedAt), "completed_at value")
	tags["owner"] = "mutated"
	testutil.RequireEqualf(t, completed.Tags["owner"], "billing", "completed tags")

	replayed, changed, err := completed.Complete(completedAt.Add(time.Minute), map[string]string{"owner": "retry"})
	testutil.RequireNoErrorf(t, err, "replay complete transaction")
	testutil.RequireFalsef(t, changed, "replay complete transaction changed")
	testutil.RequireNotNilf(t, replayed.CompletedAt, "replayed completed_at")
	testutil.RequireTruef(t, replayed.CompletedAt.Equal(completedAt), "replayed completed_at")
	testutil.RequireEqualf(t, replayed.Tags["owner"], "billing", "replayed tags")
}

func TestTransactionCompleteRejectsClosedTransaction(t *testing.T) {
	timeoutAt := time.Unix(50, 0).UTC()
	transaction := Transaction{
		Identity: identity.Transaction{
			TenantID:      "tenant",
			TransactionID: "txn",
		},
		State:     TransactionStateTimedOut,
		TimeoutAt: &timeoutAt,
	}

	_, changed, err := transaction.Complete(time.Unix(100, 0).UTC(), nil)
	testutil.RequireErrorIsf(t, err, ErrTransactionClosed, "complete timed-out transaction")
	testutil.RequireFalsef(t, changed, "complete timed-out transaction changed")
}

func TestDocumentAvailabilityPredicates(t *testing.T) {
	hot := Document{Availability: AvailabilityHot, RestoreState: RestoreStateHot}
	requireDocumentPredicates(t, hot, true, false, false, false)

	cold := Document{Availability: AvailabilityCold, RestoreState: RestoreStateCold}
	requireDocumentPredicates(t, cold, false, true, false, false)

	pending := Document{Availability: AvailabilityRestorePending, RestoreState: RestoreStateRestorePending}
	requireDocumentPredicates(t, pending, false, true, true, false)

	cryptoUnavailable := Document{Availability: AvailabilityCryptoUnavailable, RestoreState: RestoreStateCryptoUnavailable}
	requireDocumentPredicates(t, cryptoUnavailable, false, false, false, true)
}

func requireTransactionCounts(t testing.TB, transaction Transaction, documents, permanent, ephemeral uint32) {
	t.Helper()
	testutil.RequireEqualf(t, transaction.DocumentCount, documents, "document count")
	testutil.RequireEqualf(t, transaction.PermanentDocumentCount, permanent, "permanent document count")
	testutil.RequireEqualf(t, transaction.EphemeralDocumentCount, ephemeral, "ephemeral document count")
}

func requireDocumentPredicates(t testing.TB, document Document, hot, backendRestore, restoreQueued, cryptoUnavailable bool) {
	t.Helper()
	testutil.RequireEqualf(t, document.IsHot(), hot, "hot predicate")
	testutil.RequireEqualf(t, document.RequiresBackendRestore(), backendRestore, "backend restore predicate")
	testutil.RequireEqualf(t, document.IsRestoreQueued(), restoreQueued, "restore queued predicate")
	testutil.RequireEqualf(t, document.IsCryptoUnavailable(), cryptoUnavailable, "crypto unavailable predicate")
}
