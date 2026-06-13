package security_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeSecurityJSONFixture(t *testing.T, value any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.json")
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}
