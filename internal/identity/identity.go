package identity

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxTenantIDBytes      = 256
	MaxTransactionIDBytes = 512
	MaxDocumentNameBytes  = 1024
)

const (
	ReasonRequired       = "SCRAP_REQUIRED"
	ReasonTooLong        = "SCRAP_TOO_LONG"
	ReasonInvalidUTF8    = "SCRAP_INVALID_UTF8"
	ReasonControlChar    = "SCRAP_CONTROL_CHARACTER"
	ReasonAbsolutePath   = "SCRAP_ABSOLUTE_PATH"
	ReasonEmptySegment   = "SCRAP_EMPTY_PATH_SEGMENT"
	ReasonCurrentSegment = "SCRAP_CURRENT_PATH_SEGMENT"
	ReasonParentSegment  = "SCRAP_PARENT_PATH_SEGMENT"
	ReasonTrailingSlash  = "SCRAP_TRAILING_SLASH"
	ReasonDuplicateSlash = "SCRAP_DUPLICATE_SLASH"
)

type Problem struct {
	Field       string
	Reason      string
	Description string
}

type Transaction struct {
	TenantID      string
	TransactionID string
}

type Document struct {
	TenantID      string
	TransactionID string
	DocumentName  string
}

func NewTransaction(tenantID, transactionID string) (Transaction, []Problem) {
	var problems []Problem
	if problem := validateOpaqueID("tenant_id", tenantID, MaxTenantIDBytes); problem != nil {
		problems = append(problems, *problem)
	}
	if problem := validateOpaqueID("transaction_id", transactionID, MaxTransactionIDBytes); problem != nil {
		problems = append(problems, *problem)
	}
	if len(problems) > 0 {
		return Transaction{}, problems
	}
	return Transaction{TenantID: tenantID, TransactionID: transactionID}, nil
}

func NewDocument(tenantID, transactionID, documentName string) (Document, []Problem) {
	transaction, problems := NewTransaction(tenantID, transactionID)
	normalizedName, problem := NormalizeDocumentName(documentName)
	if problem != nil {
		problems = append(problems, *problem)
	}
	if len(problems) > 0 {
		return Document{}, problems
	}
	return Document{
		TenantID:      transaction.TenantID,
		TransactionID: transaction.TransactionID,
		DocumentName:  normalizedName,
	}, nil
}

func NormalizeDocumentName(documentName string) (string, *Problem) {
	return normalizeDocumentPath(documentName, false)
}

func NormalizeDocumentNamePrefix(documentNamePrefix string) (string, *Problem) {
	return normalizeDocumentPath(documentNamePrefix, true)
}

func validateOpaqueID(label, value string, maxBytes int) *Problem {
	switch {
	case value == "":
		return &Problem{Field: label, Reason: ReasonRequired, Description: label + " is required"}
	case len(value) > maxBytes:
		return &Problem{Field: label, Reason: ReasonTooLong, Description: label + " exceeds maximum byte length"}
	case !utf8.ValidString(value):
		return &Problem{Field: label, Reason: ReasonInvalidUTF8, Description: label + " must be valid UTF-8"}
	case containsControl(value):
		return &Problem{Field: label, Reason: ReasonControlChar, Description: label + " must not contain control characters"}
	default:
		return nil
	}
}

func normalizeDocumentPath(value string, allowTrailingSlash bool) (string, *Problem) {
	if value == "" {
		return "", &Problem{Field: "document_name", Reason: ReasonRequired, Description: "document_name is required"}
	}
	if !utf8.ValidString(value) {
		return "", &Problem{Field: "document_name", Reason: ReasonInvalidUTF8, Description: "document_name must be valid UTF-8"}
	}

	normalized := strings.ReplaceAll(value, "\\", "/")
	if problem := validateNormalizedDocumentPath(normalized, allowTrailingSlash); problem != nil {
		return "", problem
	}
	if problem := validateDocumentPathSegments(normalized, allowTrailingSlash); problem != nil {
		return "", problem
	}
	return normalized, nil
}

func validateNormalizedDocumentPath(normalized string, allowTrailingSlash bool) *Problem {
	switch {
	case len(normalized) > MaxDocumentNameBytes:
		return &Problem{Field: "document_name", Reason: ReasonTooLong, Description: "document_name exceeds maximum byte length"}
	case containsControl(normalized):
		return &Problem{Field: "document_name", Reason: ReasonControlChar, Description: "document_name must not contain control characters"}
	case strings.HasPrefix(normalized, "/"):
		return &Problem{Field: "document_name", Reason: ReasonAbsolutePath, Description: "document_name must be relative"}
	case strings.Contains(normalized, "//"):
		return &Problem{Field: "document_name", Reason: ReasonDuplicateSlash, Description: "document_name must not contain duplicate slashes"}
	case strings.HasSuffix(normalized, "/") && !allowTrailingSlash:
		return &Problem{Field: "document_name", Reason: ReasonTrailingSlash, Description: "document_name must not end with a slash"}
	default:
		return nil
	}
}

func validateDocumentPathSegments(normalized string, allowTrailingSlash bool) *Problem {
	segments := strings.Split(normalized, "/")
	for i, segment := range segments {
		if segment == "" {
			if allowTrailingSlash && i == len(segments)-1 {
				continue
			}
			return &Problem{Field: "document_name", Reason: ReasonEmptySegment, Description: "document_name must not contain empty path segments"}
		}
		switch segment {
		case ".":
			return &Problem{Field: "document_name", Reason: ReasonCurrentSegment, Description: "document_name must not contain current-directory segments"}
		case "..":
			return &Problem{Field: "document_name", Reason: ReasonParentSegment, Description: "document_name must not contain parent-directory segments"}
		}
	}
	return nil
}

func containsControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
