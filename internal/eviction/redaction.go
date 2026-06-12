package eviction

import "strings"

const redactedOperatorDetail = "sensitive detail redacted"

var sensitiveOperatorErrorFragments = []string{
	"backend_key",
	"backend key",
	"backend object key",
	"validation_token",
	"validation token",
	"transaction_id",
	"document_name",
	"idempotency",
	"request_id",
	"request id",
	"trace_id",
	"trace id",
	"grpc metadata",
	"auth claim",
	"auth claims",
	"peer address",
	"certificate",
	"/shards/",
	"%2fshards%2f",
	"/tmp/",
	"/home/",
	".blk",
	".idx",
}

func OperatorSafeErrorText(text string) string {
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	for _, fragment := range sensitiveOperatorErrorFragments {
		if strings.Contains(lower, fragment) {
			return redactedOperatorDetail
		}
	}
	return text
}

func OperatorSafeApplyResult(result ApplyResult) ApplyResult {
	result.Blocks = append([]ApplyBlock(nil), result.Blocks...)
	for i := range result.Blocks {
		result.Blocks[i].Error = OperatorSafeErrorText(result.Blocks[i].Error)
	}
	result.Validations = append([]ValidationBlock(nil), result.Validations...)
	for i := range result.Validations {
		result.Validations[i].Error = OperatorSafeErrorText(result.Validations[i].Error)
	}
	return result
}

func OperatorSafePlanStatus(status PlanStatus) PlanStatus {
	if status.ApplyResult != nil {
		result := OperatorSafeApplyResult(*status.ApplyResult)
		status.ApplyResult = &result
	}
	return status
}
