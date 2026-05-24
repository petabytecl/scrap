package identity

import (
	"testing"

	"github.com/petabytecl/scrap/internal/testutil"
)

func TestNewUUIDv7GeneratesValidUUIDv7(t *testing.T) {
	id, err := NewUUIDv7()
	testutil.RequireNoErrorf(t, err, "NewUUIDv7")
	if !IsUUIDv7(id) {
		t.Fatalf("generated id %q is not UUIDv7", id)
	}
	if _, err := UUIDBytes(id); err != nil {
		t.Fatalf("UUIDBytes: %v", err)
	}
}

func TestIsUUIDv7RejectsWrongVersionAndVariant(t *testing.T) {
	tests := []string{
		"018f6d86-7a22-4abc-8def-123456789abc",
		"018f6d86-7a22-7abc-7def-123456789abc",
		"not-a-uuid",
	}
	for _, value := range tests {
		if IsUUIDv7(value) {
			t.Fatalf("%q was accepted as UUIDv7", value)
		}
	}
}
