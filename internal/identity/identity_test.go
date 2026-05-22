package identity

import (
	"strings"
	"testing"
)

func TestNewDocumentNormalizesSeparators(t *testing.T) {
	doc, problems := NewDocument("tenant", "txn", `invoice\2026\001.xml`)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %#v", problems)
	}
	if doc.DocumentName != "invoice/2026/001.xml" {
		t.Fatalf("document name = %q, want normalized slash path", doc.DocumentName)
	}
}

func TestNewDocumentRejectsUnsafeDocumentNames(t *testing.T) {
	tests := map[string]string{
		"absolute":        "/invoice.xml",
		"duplicate slash": "invoice//001.xml",
		"current segment": "invoice/./001.xml",
		"parent segment":  "invoice/../001.xml",
		"trailing slash":  "invoice/",
		"control char":    "invoice/\n001.xml",
	}

	for name, documentName := range tests {
		t.Run(name, func(t *testing.T) {
			_, problems := NewDocument("tenant", "txn", documentName)
			if len(problems) == 0 {
				t.Fatalf("expected %q to be rejected", documentName)
			}
		})
	}
}

func TestNewDocumentRejectsMissingAndOversizedIdentityParts(t *testing.T) {
	_, problems := NewDocument("", strings.Repeat("t", MaxTransactionIDBytes+1), "invoice.xml")
	if len(problems) != 2 {
		t.Fatalf("problem count = %d, want 2: %#v", len(problems), problems)
	}
	if problems[0].Field != "tenant_id" || problems[0].Reason != ReasonRequired {
		t.Fatalf("first problem = %#v", problems[0])
	}
	if problems[1].Field != "transaction_id" || problems[1].Reason != ReasonTooLong {
		t.Fatalf("second problem = %#v", problems[1])
	}
}

func TestNormalizeDocumentNamePrefixAllowsFolderPrefix(t *testing.T) {
	prefix, problem := NormalizeDocumentNamePrefix(`workflow\stage/`)
	if problem != nil {
		t.Fatalf("unexpected problem: %#v", problem)
	}
	if prefix != "workflow/stage/" {
		t.Fatalf("prefix = %q, want normalized folder prefix", prefix)
	}
}
