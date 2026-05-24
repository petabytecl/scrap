package api

import (
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/storageapp"
)

func documentClassFromProto(value scrapv1.DocumentClass) storageapp.DocumentClass {
	switch value {
	case scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT:
		return storageapp.DocumentClassPermanent
	case scrapv1.DocumentClass_DOCUMENT_CLASS_EPHEMERAL:
		return storageapp.DocumentClassEphemeral
	default:
		return storageapp.DocumentClassUnspecified
	}
}

func documentClassToProto(value storageapp.DocumentClass) scrapv1.DocumentClass {
	switch value {
	case storageapp.DocumentClassPermanent:
		return scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT
	case storageapp.DocumentClassEphemeral:
		return scrapv1.DocumentClass_DOCUMENT_CLASS_EPHEMERAL
	default:
		return scrapv1.DocumentClass_DOCUMENT_CLASS_UNSPECIFIED
	}
}

func priorityClassFromProto(value scrapv1.PriorityClass) storageapp.PriorityClass {
	switch value {
	case scrapv1.PriorityClass_PRIORITY_CLASS_CRITICAL_INGEST:
		return storageapp.PriorityClassCriticalIngest
	case scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL:
		return storageapp.PriorityClassNormal
	case scrapv1.PriorityClass_PRIORITY_CLASS_BULK:
		return storageapp.PriorityClassBulk
	default:
		return storageapp.PriorityClassUnspecified
	}
}

func priorityClassToProto(value storageapp.PriorityClass) scrapv1.PriorityClass {
	switch value {
	case storageapp.PriorityClassCriticalIngest:
		return scrapv1.PriorityClass_PRIORITY_CLASS_CRITICAL_INGEST
	case storageapp.PriorityClassNormal:
		return scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL
	case storageapp.PriorityClassBulk:
		return scrapv1.PriorityClass_PRIORITY_CLASS_BULK
	default:
		return scrapv1.PriorityClass_PRIORITY_CLASS_UNSPECIFIED
	}
}

func documentAvailabilityToProto(value storageapp.DocumentAvailability) scrapv1.DocumentAvailability {
	switch value {
	case storageapp.DocumentAvailabilityHot:
		return scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_HOT
	case storageapp.DocumentAvailabilityCold:
		return scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_COLD
	case storageapp.DocumentAvailabilityRestorePending:
		return scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_RESTORE_PENDING
	case storageapp.DocumentAvailabilityCryptoUnavailable:
		return scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_CRYPTO_UNAVAILABLE
	case storageapp.DocumentAvailabilityDegradedRepair:
		return scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_DEGRADED_REPAIR
	default:
		return scrapv1.DocumentAvailability_DOCUMENT_AVAILABILITY_UNSPECIFIED
	}
}

func documentLifecycleStateToProto(value storageapp.DocumentLifecycleState) scrapv1.DocumentLifecycleState {
	switch value {
	case storageapp.DocumentLifecycleStateActive:
		return scrapv1.DocumentLifecycleState_DOCUMENT_LIFECYCLE_STATE_ACTIVE
	case storageapp.DocumentLifecycleStateTransactionCompleted:
		return scrapv1.DocumentLifecycleState_DOCUMENT_LIFECYCLE_STATE_TRANSACTION_COMPLETED
	case storageapp.DocumentLifecycleStateTombstoned:
		return scrapv1.DocumentLifecycleState_DOCUMENT_LIFECYCLE_STATE_TOMBSTONED
	case storageapp.DocumentLifecycleStatePendingReclamation:
		return scrapv1.DocumentLifecycleState_DOCUMENT_LIFECYCLE_STATE_PENDING_RECLAMATION
	default:
		return scrapv1.DocumentLifecycleState_DOCUMENT_LIFECYCLE_STATE_UNSPECIFIED
	}
}

func storageSourceToProto(value storageapp.StorageSource) scrapv1.StorageSource {
	switch value {
	case storageapp.StorageSourceLocal:
		return scrapv1.StorageSource_STORAGE_SOURCE_LOCAL
	case storageapp.StorageSourceBackend:
		return scrapv1.StorageSource_STORAGE_SOURCE_BACKEND
	default:
		return scrapv1.StorageSource_STORAGE_SOURCE_UNSPECIFIED
	}
}

func transactionStateKindToProto(value storageapp.TransactionStateKind) scrapv1.TransactionStateKind {
	switch value {
	case storageapp.TransactionStateOpen:
		return scrapv1.TransactionStateKind_TRANSACTION_STATE_KIND_OPEN
	case storageapp.TransactionStateCompleted:
		return scrapv1.TransactionStateKind_TRANSACTION_STATE_KIND_COMPLETED
	case storageapp.TransactionStateTimedOut:
		return scrapv1.TransactionStateKind_TRANSACTION_STATE_KIND_TIMED_OUT
	default:
		return scrapv1.TransactionStateKind_TRANSACTION_STATE_KIND_UNSPECIFIED
	}
}
