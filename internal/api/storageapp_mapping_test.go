package api

import (
	"testing"

	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/storageapp"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestStorageApplicationEnumMappingsCoverKnownAndFallbackValues(t *testing.T) {
	testutil.RequireEqualf(t, documentClassFromProto(scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT), storageapp.DocumentClassPermanent, "document class from proto")
	testutil.RequireEqualf(t, documentClassFromProto(scrapv1.DocumentClass_DOCUMENT_CLASS_EPHEMERAL), storageapp.DocumentClassEphemeral, "ephemeral document class from proto")
	testutil.RequireEqualf(t, documentClassFromProto(scrapv1.DocumentClass(99)), storageapp.DocumentClassUnspecified, "unknown document class from proto")
	testutil.RequireEqualf(t, documentClassToProto(storageapp.DocumentClassPermanent), scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT, "document class to proto")
	testutil.RequireEqualf(t, documentClassToProto(storageapp.DocumentClassEphemeral), scrapv1.DocumentClass_DOCUMENT_CLASS_EPHEMERAL, "ephemeral document class to proto")
	testutil.RequireEqualf(t, documentClassToProto(storageapp.DocumentClass(99)), scrapv1.DocumentClass_DOCUMENT_CLASS_UNSPECIFIED, "unknown document class to proto")

	testutil.RequireEqualf(t, priorityClassFromProto(scrapv1.PriorityClass_PRIORITY_CLASS_CRITICAL_INGEST), storageapp.PriorityClassCriticalIngest, "critical priority from proto")
	testutil.RequireEqualf(t, priorityClassFromProto(scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL), storageapp.PriorityClassNormal, "normal priority from proto")
	testutil.RequireEqualf(t, priorityClassFromProto(scrapv1.PriorityClass_PRIORITY_CLASS_BULK), storageapp.PriorityClassBulk, "bulk priority from proto")
	testutil.RequireEqualf(t, priorityClassFromProto(scrapv1.PriorityClass(99)), storageapp.PriorityClassUnspecified, "unknown priority from proto")
	testutil.RequireEqualf(t, priorityClassToProto(storageapp.PriorityClassCriticalIngest), scrapv1.PriorityClass_PRIORITY_CLASS_CRITICAL_INGEST, "critical priority to proto")
	testutil.RequireEqualf(t, priorityClassToProto(storageapp.PriorityClassNormal), scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL, "normal priority to proto")
	testutil.RequireEqualf(t, priorityClassToProto(storageapp.PriorityClassBulk), scrapv1.PriorityClass_PRIORITY_CLASS_BULK, "bulk priority to proto")
	testutil.RequireEqualf(t, priorityClassToProto(storageapp.PriorityClass(99)), scrapv1.PriorityClass_PRIORITY_CLASS_UNSPECIFIED, "unknown priority to proto")

	for _, tc := range []struct {
		name string
		in   storageapp.DocumentAvailability
		want scrapv1.DocumentAvailability
	}{
		{name: "hot", in: storageapp.DocumentAvailabilityHot, want: scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_HOT},
		{name: "cold", in: storageapp.DocumentAvailabilityCold, want: scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_COLD},
		{name: "restore pending", in: storageapp.DocumentAvailabilityRestorePending, want: scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_RESTORE_PENDING},
		{name: "crypto unavailable", in: storageapp.DocumentAvailabilityCryptoUnavailable, want: scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_CRYPTO_UNAVAILABLE},
		{name: "degraded repair", in: storageapp.DocumentAvailabilityDegradedRepair, want: scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_DEGRADED_REPAIR},
		{name: "unknown", in: storageapp.DocumentAvailability(99), want: scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_UNSPECIFIED},
	} {
		t.Run("availability "+tc.name, func(t *testing.T) {
			testutil.RequireEqualf(t, documentAvailabilityToProto(tc.in), tc.want, "availability mapping")
		})
	}

	for _, tc := range []struct {
		name string
		in   storageapp.DocumentLifecycleState
		want scrapv1.DocumentLifecycleState
	}{
		{name: "active", in: storageapp.DocumentLifecycleStateActive, want: scrapv1.DocumentLifecycleState_DOCUMENT_LIFECYCLE_STATE_ACTIVE},
		{name: "transaction completed", in: storageapp.DocumentLifecycleStateTransactionCompleted, want: scrapv1.DocumentLifecycleState_DOCUMENT_LIFECYCLE_STATE_TRANSACTION_COMPLETED},
		{name: "tombstoned", in: storageapp.DocumentLifecycleStateTombstoned, want: scrapv1.DocumentLifecycleState_DOCUMENT_LIFECYCLE_STATE_TOMBSTONED},
		{name: "pending reclamation", in: storageapp.DocumentLifecycleStatePendingReclamation, want: scrapv1.DocumentLifecycleState_DOCUMENT_LIFECYCLE_STATE_PENDING_RECLAMATION},
		{name: "unknown", in: storageapp.DocumentLifecycleState(99), want: scrapv1.DocumentLifecycleState_DOCUMENT_LIFECYCLE_STATE_UNSPECIFIED},
	} {
		t.Run("lifecycle "+tc.name, func(t *testing.T) {
			testutil.RequireEqualf(t, documentLifecycleStateToProto(tc.in), tc.want, "lifecycle mapping")
		})
	}

	testutil.RequireEqualf(t, storageSourceToProto(storageapp.StorageSourceLocal), scrapv1.StorageSource_STORAGE_SOURCE_LOCAL, "local source")
	testutil.RequireEqualf(t, storageSourceToProto(storageapp.StorageSourceBackend), scrapv1.StorageSource_STORAGE_SOURCE_BACKEND, "backend source")
	testutil.RequireEqualf(t, storageSourceToProto(storageapp.StorageSource(99)), scrapv1.StorageSource_STORAGE_SOURCE_UNSPECIFIED, "unknown source")
	testutil.RequireEqualf(t, transactionStateKindToProto(storageapp.TransactionStateOpen), scrapv1.TransactionStateKind_TRANSACTION_STATE_KIND_OPEN, "open transaction")
	testutil.RequireEqualf(t, transactionStateKindToProto(storageapp.TransactionStateCompleted), scrapv1.TransactionStateKind_TRANSACTION_STATE_KIND_COMPLETED, "completed transaction")
	testutil.RequireEqualf(t, transactionStateKindToProto(storageapp.TransactionStateTimedOut), scrapv1.TransactionStateKind_TRANSACTION_STATE_KIND_TIMED_OUT, "timed out transaction")
	testutil.RequireEqualf(t, transactionStateKindToProto(storageapp.TransactionStateKind(99)), scrapv1.TransactionStateKind_TRANSACTION_STATE_KIND_UNSPECIFIED, "unknown transaction")
}
