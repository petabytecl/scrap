package localstorage

import (
	"context"

	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/storageapp"
)

type verificationEngineRole interface {
	startByteServingVerifier(context.Context)
	waitByteServingReady(context.Context) error
	readVerifiedBackendRange(context.Context, metastore.Document, metastore.UploadIntent, storageapp.ReadRange) ([]byte, error)
	verifyBackendEnvelope(context.Context, metastore.UploadIntent) error
	openBackendEnvelope(context.Context, metastore.UploadIntent) (backendEnvelopeMaterial, error)
	openVerifiedEncryptedBackendBlock(context.Context, metastore.UploadIntent, backendEnvelopeMaterial) (verifiedEncryptedBackendBlock, error)
}

type verificationEngine struct {
	*Application
}
