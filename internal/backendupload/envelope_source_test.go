package backendupload

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/cryptoenv"
	storagev1 "github.com/petabytecl/scrap/internal/gen/scrap/storage/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/storageformat"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestTransitBlockEnvelopeSourceStoresWrappedKeyMaterial(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	store := openTestBackendStore(t)
	record, err := blocks.Append(ctx, bytes.NewReader([]byte("transit envelope bytes")))
	testutil.RequireNoErrorf(t, err, "append block")
	if _, err := blocks.SealCurrent(ctx); err != nil {
		t.Fatalf("seal block: %v", err)
	}
	transit := cryptoenv.NewFakeTransit(map[string]uint32{"transit/backend": 4})
	intent := testUploadIntent(record.BlockID)
	intent.EnvelopeObjectKey = "objects/" + record.BlockID + ".env"

	if _, err := (Uploader{
		Backend: store,
		Source:  LocalBlockSource{Blocks: blocks},
		Index:   staticBlockIndexSource{body: []byte("index bytes")},
		Envelope: TransitBlockEnvelopeSource{
			Transit: transit,
			CellID:  "cell-a",
			KeyID:   "transit/backend",
		},
	}).UploadBlock(ctx, intent); err != nil {
		t.Fatalf("upload block: %v", err)
	}

	var got bytes.Buffer
	testutil.RequireNoErrorf(t, store.ReadObjectRange(ctx, intent.EnvelopeObjectKey, backend.Range{}, &got), "read envelope object")
	envelope, err := storageformat.UnmarshalEnvelopeRecord(got.Bytes())
	testutil.RequireNoErrorf(t, err, "unmarshal envelope")
	key, err := transit.UnwrapDataKey(ctx, cryptoenv.UnwrapDataKeyRequest{
		KeyID:      envelope.GetKeyId(),
		KeyVersion: envelope.GetKeyVersion(),
		WrappedDEK: envelope.GetWrappedDek(),
		AAD:        envelope.GetAadContext(),
		Algorithm:  envelope.GetDekAlgorithm(),
	})
	testutil.RequireNoErrorf(t, err, "unwrap envelope")
	if bytes.Equal(envelope.GetWrappedDek(), key.PlaintextDEK) {
		t.Fatal("envelope object stored plaintext DEK")
	}
	if envelope.GetKeyId() != "transit/backend" ||
		envelope.GetKeyVersion() != 4 ||
		envelope.GetAeadAlgorithm() != cryptoenv.DefaultAEADAlgorithm {
		t.Fatalf("envelope = %#v, want transit key/version and encrypted AEAD algorithm", envelope)
	}
}

func TestTransitUploadEncryptsBackendBlockAndIndexesCiphertextFrames(t *testing.T) {
	ctx := context.Background()
	blocks := openTestBlockStore(t)
	store := openTestBackendStore(t)
	metadataStore := openTestMetastore(t)
	data := bytes.Repeat([]byte("backend ciphertext frame "), 8)
	record, err := blocks.Append(ctx, bytes.NewReader(data))
	testutil.RequireNoErrorf(t, err, "append block")
	sealTestCurrentBlock(ctx, t, blocks)
	originalBlock, err := os.ReadFile(blocks.BlockPath(record.BlockID))
	testutil.RequireNoErrorf(t, err, "read local block")
	document := metastore.Document{
		Identity: identity.Document{
			TenantID:      "tenant",
			TransactionID: "txn",
			DocumentName:  "encrypted.xml",
		},
		DocumentClass:    metastore.DocumentClassPermanent,
		PriorityClass:    metastore.PriorityClassNormal,
		Length:           record.StoredLength,
		LogicalSHA256:    record.LogicalSHA256,
		StoredSHA256:     record.StoredSHA256,
		CreatedByService: "billing-etl",
		CreatedAt:        time.Unix(100, 0).UTC(),
		FinalizedAt:      time.Unix(100, 0).UTC(),
		Availability:     metastore.AvailabilityHot,
		LifecycleState:   metastore.LifecycleStateActive,
		RestoreState:     metastore.RestoreStateHot,
		UploadState:      metastore.UploadStatePending,
		Location:         metastoreLocationFromBlockRecord(record),
	}
	testutil.RequireNoErrorf(t, metadataStore.PutDocument(document), "put document metadata")
	intent := testUploadIntent(record.BlockID)
	intent.EnvelopeObjectKey = "objects/" + record.BlockID + ".env"
	transit := cryptoenv.NewFakeTransit(map[string]uint32{"transit/backend": 4})

	result, err := (Uploader{
		Backend: store,
		Source:  LocalBlockSource{Blocks: blocks},
		Envelope: TransitBlockEnvelopeSource{
			Transit: transit,
			CellID:  "cell-a",
			KeyID:   "transit/backend",
		},
		Index: LocalBlockIndexSource{Documents: metadataStore, ShardID: "local"},
	}).UploadBlock(ctx, intent)
	testutil.RequireNoErrorf(t, err, "upload block")

	uploadedBlock := readBackendObject(ctx, t, store, intent.BackendObjectKey)
	if bytes.Equal(uploadedBlock, originalBlock) {
		t.Fatal("backend block object stored plaintext local block")
	}
	if bytes.Contains(uploadedBlock, data) {
		t.Fatal("backend block object contains client-visible plaintext")
	}
	envelope, err := storageformat.UnmarshalEnvelopeRecord(readBackendObject(ctx, t, store, intent.EnvelopeObjectKey))
	testutil.RequireNoErrorf(t, err, "unmarshal envelope")
	testutil.RequireEqualf(t, envelope.GetAeadAlgorithm(), cryptoenv.DefaultAEADAlgorithm, "envelope AEAD algorithm")

	index, err := storageformat.UnmarshalBlockIndex(readBackendObject(ctx, t, store, intent.IndexObjectKey))
	testutil.RequireNoErrorf(t, err, "unmarshal block index")
	testutil.RequireEqualf(t, index.GetBlockLength(), result.Block.Length, "index block length")
	testutil.RequireTruef(t, bytes.Equal(index.GetBlockSha256(), result.Block.SHA256[:]), "index block sha256")
	testutil.RequireEqualf(t, len(index.GetFrames()), 1, "index frame count")
	frame := index.GetFrames()[0]
	testutil.RequireEqualf(t, frame.GetEncryptionMode(), storagev1.EncryptionMode_ENCRYPTION_MODE_AES_256_GCM, "frame encryption mode")
	testutil.RequireEqualf(t, len(frame.GetAuthTag()), 16, "frame auth tag length")
	testutil.RequireEqualf(t, frame.GetStoredOffset(), uint64(0), "ciphertext frame offset")
	testutil.RequireTruef(t, frame.GetStoredLength() > frame.GetPlaintextLength(), "stored frame length = %d, want ciphertext tag overhead over plaintext length %d", frame.GetStoredLength(), frame.GetPlaintextLength())
	testutil.RequireFalsef(t, bytes.Equal(frame.GetStoredSha256(), frame.GetPlaintextSha256()), "stored and plaintext frame checksums should differ")
	framePlaintextEnd := frame.GetPlaintextOffset() + frame.GetPlaintextLength()
	documentEnd := record.StoredOffset + record.StoredLength
	testutil.RequireTruef(t, frame.GetPlaintextOffset() <= record.StoredOffset && framePlaintextEnd >= documentEnd, "encrypted frame plaintext range [%d,%d) does not cover document range [%d,%d)", frame.GetPlaintextOffset(), framePlaintextEnd, record.StoredOffset, documentEnd)
}

func TestTransitEncryptBlockPayloadSplitsFramesAndRemovesTempFile(t *testing.T) {
	ctx := context.Background()
	data := bytes.Repeat([]byte("x"), blockstore.DefaultFrameSize+17)
	tempDir := t.TempDir()
	source := TransitBlockEnvelopeSource{
		Transit: cryptoenv.NewFakeTransit(map[string]uint32{"transit/backend": 4}),
		CellID:  "cell-a",
		KeyID:   "transit/backend",
		TempDir: tempDir,
	}

	encrypted, err := source.EncryptBlockPayload(ctx, testUploadIntent("block-1"), bytes.NewReader(data))
	testutil.RequireNoErrorf(t, err, "encrypt block payload")
	testutil.RequireEqualf(t, len(encrypted.Frames), 2, "encrypted frame count")
	first := encrypted.Frames[0]
	second := encrypted.Frames[1]
	testutil.RequireEqualf(t, first.FrameIndex, uint32(0), "first frame index")
	testutil.RequireEqualf(t, first.PlaintextOffset, uint64(0), "first plaintext offset")
	testutil.RequireEqualf(t, second.FrameIndex, uint32(1), "second frame index")
	testutil.RequireEqualf(t, second.PlaintextOffset, first.PlaintextLength, "second plaintext offset")
	testutil.RequireEqualf(t, second.StoredOffset, first.StoredLength, "second stored offset")

	tempPayload, ok := encrypted.Reader.(removeOnCloseFile)
	testutil.RequireTruef(t, ok, "encrypted payload reader type = %T", encrypted.Reader)
	testutil.RequireTruef(t, strings.HasPrefix(tempPayload.path, tempDir), "temp payload path %q did not use configured dir %q", tempPayload.path, tempDir)
	ciphertext, err := io.ReadAll(encrypted.Reader)
	testutil.RequireNoErrorf(t, err, "read encrypted payload")
	testutil.RequireNoErrorf(t, encrypted.Reader.Close(), "close encrypted payload")
	if _, err := os.Stat(tempPayload.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp payload file still present after close: %v", err)
	}
	testutil.RequireFalsef(t, bytes.Contains(ciphertext, []byte("xxxxxxxxxxxxxxxx")), "ciphertext contained repeated plaintext marker")

	plaintextDEK, err := cryptoenv.UnwrapEnvelopeDataKey(ctx, source.Transit, encrypted.Envelope)
	testutil.RequireNoErrorf(t, err, "unwrap payload envelope")
	var opened bytes.Buffer
	for _, frame := range encrypted.Frames {
		start := frame.StoredOffset
		end := start + frame.StoredLength
		plaintext, err := cryptoenv.OpenPayloadFrame(plaintextDEK, encrypted.Envelope, frame.FrameIndex, frame.PlaintextOffset, frame.PlaintextLength, ciphertext[start:end])
		testutil.RequireNoErrorf(t, err, "open encrypted frame %d", frame.FrameIndex)
		opened.Write(plaintext)
	}
	testutil.RequireTruef(t, bytes.Equal(opened.Bytes(), data), "decrypted payload did not match source")
}

func TestTransitEncryptBlockPayloadRejectsEmptyAndReaderErrors(t *testing.T) {
	ctx := context.Background()
	source := TransitBlockEnvelopeSource{
		Transit: cryptoenv.NewFakeTransit(map[string]uint32{"transit/backend": 4}),
		KeyID:   "transit/backend",
	}

	_, err := source.EncryptBlockPayload(ctx, testUploadIntent("block-1"), bytes.NewReader(nil))
	if err == nil || !strings.Contains(err.Error(), "encrypted block payload is empty") {
		t.Fatalf("empty payload error = %v, want encrypted payload empty", err)
	}

	readErr := errors.New("reader failed")
	_, err = source.EncryptBlockPayload(ctx, testUploadIntent("block-1"), failingReader{err: readErr})
	if !errors.Is(err, readErr) {
		t.Fatalf("reader error = %v, want %v", err, readErr)
	}
}

func TestWriteEncryptedBlockFramePropagatesWriterFailures(t *testing.T) {
	ctx := context.Background()
	transit := cryptoenv.NewFakeTransit(map[string]uint32{"transit/backend": 4})
	material, err := cryptoenv.CreateEnvelopeRecord(ctx, transit, cryptoenv.EnvelopeRequest{
		BlockID:     "block-1",
		BlockObject: backend.Object{Key: "objects/block-1.blk"},
		KeyID:       "transit/backend",
		CreatedAt:   time.Unix(100, 0).UTC(),
	})
	testutil.RequireNoErrorf(t, err, "create envelope")

	writeErr := errors.New("write failed")
	_, err = writeEncryptedBlockFrame(errorWriter{err: writeErr}, material, 0, 0, 0, []byte("payload"))
	if !errors.Is(err, writeErr) {
		t.Fatalf("write error = %v, want %v", err, writeErr)
	}
	_, err = writeEncryptedBlockFrame(shortWriter{}, material, 0, 0, 0, []byte("payload"))
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write error = %v, want %v", err, io.ErrShortWrite)
	}
}

type failingReader struct {
	err error
}

func (r failingReader) Read([]byte) (int, error) {
	return 0, r.err
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) {
	return len(p) - 1, nil
}
