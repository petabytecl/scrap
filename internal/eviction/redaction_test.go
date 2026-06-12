package eviction

import "testing"

func TestOperatorSafeApplyResultRedactsSensitiveErrorDetails(t *testing.T) {
	result := ApplyResult{
		PlanID: "plan-redaction",
		Blocks: []ApplyBlock{{
			BlockID: 1,
			Error:   "restore failed for backend_key=cell/shards/7/1.blk validation_token=secret",
		}},
		Validations: []ValidationBlock{{
			BlockID: 1,
			Error:   "transaction_id=tx-1 document_name=doc.xml trace_id=abc /tmp/block.idx",
		}},
	}

	got := OperatorSafeApplyResult(result)
	if got.Blocks[0].Error != redactedOperatorDetail {
		t.Fatalf("block error = %q, want redacted", got.Blocks[0].Error)
	}
	if got.Validations[0].Error != redactedOperatorDetail {
		t.Fatalf("validation error = %q, want redacted", got.Validations[0].Error)
	}
	if result.Blocks[0].Error == redactedOperatorDetail || result.Validations[0].Error == redactedOperatorDetail {
		t.Fatalf("OperatorSafeApplyResult mutated input: %+v", result)
	}
}

func TestOperatorSafeErrorTextPreservesBoundedMessage(t *testing.T) {
	const msg = "Backend restore failed"
	if got := OperatorSafeErrorText(msg); got != msg {
		t.Fatalf("OperatorSafeErrorText = %q, want %q", got, msg)
	}
}
