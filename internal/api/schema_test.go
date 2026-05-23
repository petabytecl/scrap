package api_test

import (
	"testing"

	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestPublicDocumentServiceShape(t *testing.T) {
	service := requireService(t, scrapv1.File_scrap_v1_document_proto, "DocumentService")

	requireMethod(t, service, "WriteDocument", "scrap.v1.WriteDocumentRequest", "scrap.v1.WriteDocumentResponse", true, false)
	requireMethod(t, service, "HeadDocument", "scrap.v1.HeadDocumentRequest", "scrap.v1.HeadDocumentResponse", false, false)
	requireMethod(t, service, "ReadDocument", "scrap.v1.ReadDocumentRequest", "scrap.v1.ReadDocumentResponse", false, true)
	requireMethod(t, service, "FindDocuments", "scrap.v1.FindDocumentsRequest", "scrap.v1.FindDocumentsResponse", false, false)

	request := scrapv1.File_scrap_v1_document_proto.Messages().ByName("WriteDocumentRequest")
	if request == nil {
		t.Fatal("WriteDocumentRequest descriptor missing")
	}
	if request.Oneofs().ByName("message") == nil {
		t.Fatal("WriteDocumentRequest must use the ordered init/chunk oneof")
	}
	requirePresence(t, scrapv1.File_scrap_v1_document_proto.Messages().ByName("WriteDocumentInit"), "expected_length")
	requirePresence(t, scrapv1.File_scrap_v1_document_proto.Messages().ByName("WriteDocumentInit"), "expected_sha256")
	requirePresence(t, scrapv1.File_scrap_v1_identity_proto.Messages().ByName("ReadRange"), "length")
}

func TestPublicTransactionServiceShape(t *testing.T) {
	service := requireService(t, scrapv1.File_scrap_v1_transaction_proto, "TransactionService")

	requireMethod(t, service, "CompleteTransaction", "scrap.v1.CompleteTransactionRequest", "scrap.v1.CompleteTransactionResponse", false, false)
	requireMethod(t, service, "GetTransaction", "scrap.v1.GetTransactionRequest", "scrap.v1.GetTransactionResponse", false, false)
	requirePresence(t, scrapv1.File_scrap_v1_transaction_proto.Messages().ByName("TransactionState"), "completed_at")
	requirePresence(t, scrapv1.File_scrap_v1_transaction_proto.Messages().ByName("TransactionState"), "timeout_at")
}

func TestPublicErrorDetailsShape(t *testing.T) {
	for _, name := range []protoreflect.Name{
		"RestorePendingDetail",
		"CryptoUnavailableDetail",
		"IntegrityFailureDetail",
		"DuplicateWriteDetail",
		"UnsafeCapacityDetail",
		"RetryHint",
	} {
		if scrapv1.File_scrap_v1_errors_proto.Messages().ByName(name) == nil {
			t.Fatalf("%s descriptor missing", name)
		}
	}
	requirePresence(t, scrapv1.File_scrap_v1_errors_proto.Messages().ByName("RetryHint"), "retry_at")
	requirePresence(t, scrapv1.File_scrap_v1_errors_proto.Messages().ByName("RetryHint"), "retry_delay")
}

func TestAdminServiceCatalogShape(t *testing.T) {
	expected := map[protoreflect.Name][]protoreflect.Name{
		"InspectService": {
			"GetClusterSummary",
			"GetShard",
			"GetDocument",
			"GetBlock",
			"GetMember",
			"GetCapacityRunway",
		},
		"OperationService": {
			"GetOperation",
			"WatchOperation",
			"ListOperations",
			"CancelOperation",
		},
		"RestoreService": {
			"PlanRestore",
			"StartRestore",
			"PlanPrewarm",
			"StartPrewarm",
		},
		"RepairService": {
			"GetRepairQueue",
			"PlanRepair",
			"StartRepair",
			"PlanScrub",
			"StartScrub",
		},
		"MemberService": {
			"CordonMember",
			"UncordonMember",
			"GetEvictionSafety",
			"PlanDrain",
			"StartDrain",
		},
		"LifecycleService": {
			"PlanTombstone",
			"StartTombstone",
		},
		"KeyService": {
			"PlanKeyRotation",
			"StartKeyRotation",
		},
		"CapacityService": {
			"PlanCapacityOverride",
			"StartCapacityOverride",
		},
		"DisasterRecoveryService": {
			"GetRecoveryReadiness",
			"PlanRecovery",
			"StartMetadataRestore",
			"StartCopyVerify",
			"StartDRDrill",
		},
	}

	for serviceName, methods := range expected {
		service := requireService(t, adminv1.File_scrap_admin_v1_admin_proto, serviceName)
		for _, methodName := range methods {
			if service.Methods().ByName(methodName) == nil {
				t.Fatalf("%s.%s descriptor missing", serviceName, methodName)
			}
		}
	}
}

func TestAdminOperationModelShape(t *testing.T) {
	target := adminv1.File_scrap_admin_v1_operations_proto.Messages().ByName("Target")
	if target == nil {
		t.Fatal("Target descriptor missing")
	}
	if target.Oneofs().ByName("target") == nil {
		t.Fatal("Target must use a typed target oneof")
	}

	operation := adminv1.File_scrap_admin_v1_operations_proto.Messages().ByName("Operation")
	requirePresence(t, operation, "started_at")
	requirePresence(t, operation, "finished_at")
	requirePresence(t, operation, "last_error")
}

func TestAdminMutatingRequestsCarryOperationID(t *testing.T) {
	for _, name := range []protoreflect.Name{
		"StartRestoreRequest",
		"StartPrewarmRequest",
		"StartRepairRequest",
		"StartScrubRequest",
		"CordonMemberRequest",
		"UncordonMemberRequest",
		"StartDrainRequest",
		"StartTombstoneRequest",
		"StartKeyRotationRequest",
		"StartCapacityOverrideRequest",
		"StartMetadataRestoreRequest",
		"StartCopyVerifyRequest",
		"StartDRDrillRequest",
	} {
		message := adminv1.File_scrap_admin_v1_admin_proto.Messages().ByName(name)
		if message == nil {
			t.Fatalf("%s descriptor missing", name)
		}
		field := message.Fields().ByName("operation_id")
		if field == nil {
			t.Fatalf("%s.operation_id descriptor missing", name)
		}
		if field.Kind() != protoreflect.StringKind {
			t.Fatalf("%s.operation_id kind = %s, want string", name, field.Kind())
		}
	}
}

func requireService(t *testing.T, file protoreflect.FileDescriptor, name protoreflect.Name) protoreflect.ServiceDescriptor {
	t.Helper()
	service := file.Services().ByName(name)
	if service == nil {
		t.Fatalf("%s descriptor missing", name)
	}
	return service
}

func requireMethod(
	t *testing.T,
	service protoreflect.ServiceDescriptor,
	name protoreflect.Name,
	input protoreflect.FullName,
	output protoreflect.FullName,
	clientStreaming bool,
	serverStreaming bool,
) {
	t.Helper()
	method := service.Methods().ByName(name)
	if method == nil {
		t.Fatalf("%s.%s descriptor missing", service.Name(), name)
	}
	if method.Input().FullName() != input {
		t.Fatalf("%s.%s input = %s, want %s", service.Name(), name, method.Input().FullName(), input)
	}
	if method.Output().FullName() != output {
		t.Fatalf("%s.%s output = %s, want %s", service.Name(), name, method.Output().FullName(), output)
	}
	if method.IsStreamingClient() != clientStreaming {
		t.Fatalf("%s.%s client streaming = %v, want %v", service.Name(), name, method.IsStreamingClient(), clientStreaming)
	}
	if method.IsStreamingServer() != serverStreaming {
		t.Fatalf("%s.%s server streaming = %v, want %v", service.Name(), name, method.IsStreamingServer(), serverStreaming)
	}
}

func requirePresence(t *testing.T, message protoreflect.MessageDescriptor, fieldName protoreflect.Name) {
	t.Helper()
	if message == nil {
		t.Fatalf("%s field owner descriptor missing", fieldName)
	}
	field := message.Fields().ByName(fieldName)
	if field == nil {
		t.Fatalf("%s.%s descriptor missing", message.Name(), fieldName)
	}
	if !field.HasPresence() {
		t.Fatalf("%s.%s must preserve field presence", message.Name(), fieldName)
	}
}
