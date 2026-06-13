//go:build integration

package integration_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/encryption"
	"github.com/petabytecl/scrap/test/integration/testinfra"
	scrapopenbao "github.com/petabytecl/scrap/test/integration/testinfra/openbao"
)

//goland:noinspection ALL
func TestIntegrationOpenBaoTransitContainerRoundTrip(t *testing.T) {
	ctx := integrationTestContext(t)
	openBao, err := scrapopenbao.Run(ctx, scrapopenbao.DefaultImage)
	if openBao != nil {
		testinfra.CleanupContainer(t, openBao)
	}
	if err != nil {
		t.Fatalf("start OpenBao testcontainer: %v", err)
	}

	transitConfig, err := openBao.TransitConfig(ctx)
	if err != nil {
		t.Fatalf("build OpenBao Transit config: %v", err)
	}
	transit, err := encryption.NewOpenBaoTransit(transitConfig)
	if err != nil {
		t.Fatalf("NewOpenBaoTransit: %v", err)
	}
	if !encryption.ProductionCapable(transit) {
		t.Fatal("OpenBao Transit should be production capable")
	}

	ready, err := transit.Readiness(ctx)
	if err != nil {
		t.Fatalf("Readiness: %v", err)
	}
	if !ready.Ready || ready.LatestVersion != 1 {
		t.Fatalf("Readiness = %+v, want version 1 ready key", ready)
	}

	requestContext := []byte("tx/doc")
	dataKey, err := transit.GenerateDataKey(ctx, encryption.GenerateDataKeyRequest{
		Context: requestContext,
		Bits:    256,
	})
	if err != nil {
		t.Fatalf("GenerateDataKey: %v", err)
	}
	if len(dataKey.Plaintext) != 32 || !strings.HasPrefix(dataKey.WrappedKey, "vault:v1:") {
		t.Fatalf("dataKey = %+v, want 256-bit vault:v1 wrapped key", dataKey)
	}

	unwrapped, err := transit.UnwrapDataKey(ctx, encryption.UnwrapDataKeyRequest{
		WrappedKey: dataKey.WrappedKey,
		Context:    requestContext,
	})
	if err != nil {
		t.Fatalf("UnwrapDataKey: %v", err)
	}
	if !bytes.Equal(unwrapped.Plaintext, dataKey.Plaintext) {
		t.Fatal("unwrapped plaintext did not match generated data key")
	}

	if err := openBao.RotateTransitKey(ctx); err != nil {
		t.Fatalf("RotateTransitKey: %v", err)
	}
	rewrapped, err := transit.RewrapDataKey(ctx, encryption.RewrapDataKeyRequest{
		WrappedKey: dataKey.WrappedKey,
		Context:    requestContext,
	})
	if err != nil {
		t.Fatalf("RewrapDataKey: %v", err)
	}
	if !rewrapped.Changed || !strings.HasPrefix(rewrapped.WrappedKey, "vault:v2:") {
		t.Fatalf("rewrapped = %+v, want changed vault:v2 key", rewrapped)
	}
}
