package security

import (
	"crypto/x509"
	"testing"
)

func TestHasExtKeyUsageRequiresExplicitUsage(t *testing.T) {
	tests := []struct {
		name  string
		usage []x509.ExtKeyUsage
		want  bool
	}{
		{name: "empty is rejected", usage: nil, want: false},
		{name: "unknown-only is rejected", usage: []x509.ExtKeyUsage{}, want: false},
		{name: "matching usage", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, want: true},
		{name: "any usage", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}, want: true},
		{name: "wrong usage only", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, want: false},
		{name: "matching among many", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cert := &x509.Certificate{ExtKeyUsage: tt.usage}
			if got := hasExtKeyUsage(cert, x509.ExtKeyUsageClientAuth); got != tt.want {
				t.Fatalf("hasExtKeyUsage = %v, want %v", got, tt.want)
			}
		})
	}
}
