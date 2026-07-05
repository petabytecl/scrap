package encryption_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/petabytecl/scrap/internal/encryption"
)

func TestDocumentEncryptorStreamsFramesAndRoundTrips(t *testing.T) {
	identity := encryption.DocumentIdentity{TransactionID: "tx-1", DocumentName: "doc-a"}
	body := bytes.Repeat([]byte("scrap-streaming-payload-"), 20_000) // multi-frame
	cfg := encryption.DocumentConfig{Transit: encryption.NewFakeTransit(encryption.FakeConfig{})}

	encryptor, err := encryption.NewDocumentEncryptor(context.Background(), cfg, identity, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewDocumentEncryptor: %v", err)
	}
	defer encryptor.Close()

	frames := collectStreamedFrames(t, encryptor)
	if len(frames) < 2 {
		t.Fatalf("frames = %d, want multi-frame stream", len(frames))
	}

	info, err := encryptor.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if int(info.FrameCount) != len(frames) || info.PlaintextSize != int64(len(body)) {
		t.Fatalf("info = %+v, want %d frames and %d plaintext bytes", info, len(frames), len(body))
	}

	decryptor, err := encryption.NewDocumentDecryptor(context.Background(), cfg.Transit, identity, info.Envelope, info.PlaintextSHA256, info.PlaintextSize)
	if err != nil {
		t.Fatalf("NewDocumentDecryptor: %v", err)
	}
	defer decryptor.Close()

	reader := decryptor.Reader(encryption.NewSliceFrameSource(frames))
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read streamed plaintext: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close streamed reader: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatal("streamed round-tripped plaintext does not match input")
	}
}

func collectStreamedFrames(t *testing.T, encryptor *encryption.DocumentEncryptor) [][]byte {
	t.Helper()
	var frames [][]byte
	sawLast := false
	for {
		frame, last, err := encryptor.NextFrame()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("NextFrame: %v", err)
		}
		if sawLast {
			t.Fatal("NextFrame produced a frame after the last frame")
		}
		frames = append(frames, frame)
		sawLast = last
	}
	if !sawLast {
		t.Fatal("stream ended without a last frame")
	}
	return frames
}

func TestDocumentEncryptorFinalizeBeforeLastFrameFails(t *testing.T) {
	identity := encryption.DocumentIdentity{TransactionID: "tx-1", DocumentName: "doc-a"}
	cfg := encryption.DocumentConfig{Transit: encryption.NewFakeTransit(encryption.FakeConfig{})}
	encryptor, err := encryption.NewDocumentEncryptor(context.Background(), cfg, identity, bytes.NewReader([]byte("body")))
	if err != nil {
		t.Fatalf("NewDocumentEncryptor: %v", err)
	}
	defer encryptor.Close()

	if _, err := encryptor.Finalize(); !errors.Is(err, encryption.ErrInvalidEnvelope) {
		t.Fatalf("Finalize before last frame = %v, want ErrInvalidEnvelope", err)
	}
}

func TestDocumentDecryptorVerifyDetectsTamperAndTruncation(t *testing.T) {
	identity := encryption.DocumentIdentity{TransactionID: "tx-1", DocumentName: "doc-a"}
	body := bytes.Repeat([]byte("scrap-streaming-payload-"), 20_000)
	cfg, doc := encryptTestDocument(t, identity, body)

	newDecryptor := func(t *testing.T) *encryption.DocumentDecryptor {
		t.Helper()
		decryptor, err := encryption.NewDocumentDecryptor(context.Background(), cfg.Transit, identity, doc.Envelope, doc.PlaintextSHA256, doc.PlaintextSize)
		if err != nil {
			t.Fatalf("NewDocumentDecryptor: %v", err)
		}
		t.Cleanup(decryptor.Close)
		return decryptor
	}

	t.Run("clean verify passes", func(t *testing.T) {
		if err := newDecryptor(t).Verify(encryption.NewSliceFrameSource(doc.Frames)); err != nil {
			t.Fatalf("Verify clean frames: %v", err)
		}
	})

	t.Run("tampered frame rejected", func(t *testing.T) {
		frames := cloneTestFrames(doc.Frames)
		frames[1][0] ^= 0xFF
		err := newDecryptor(t).Verify(encryption.NewSliceFrameSource(frames))
		if !errors.Is(err, encryption.ErrIntegrity) {
			t.Fatalf("Verify tampered frame = %v, want ErrIntegrity", err)
		}
	})

	t.Run("truncated stream rejected", func(t *testing.T) {
		frames := cloneTestFrames(doc.Frames[:len(doc.Frames)-1])
		err := newDecryptor(t).Verify(encryption.NewSliceFrameSource(frames))
		if !errors.Is(err, encryption.ErrIntegrity) {
			t.Fatalf("Verify truncated stream = %v, want ErrIntegrity", err)
		}
	})
}

func TestDocumentDecryptReaderFailsMidStreamOnTamperedLaterFrame(t *testing.T) {
	identity := encryption.DocumentIdentity{TransactionID: "tx-1", DocumentName: "doc-a"}
	body := bytes.Repeat([]byte("scrap-streaming-payload-"), 20_000)
	cfg, doc := encryptTestDocument(t, identity, body)

	frames := cloneTestFrames(doc.Frames)
	frames[len(frames)-1][0] ^= 0xFF

	decryptor, err := encryption.NewDocumentDecryptor(context.Background(), cfg.Transit, identity, doc.Envelope, doc.PlaintextSHA256, doc.PlaintextSize)
	if err != nil {
		t.Fatalf("NewDocumentDecryptor: %v", err)
	}
	defer decryptor.Close()

	reader := decryptor.Reader(encryption.NewSliceFrameSource(frames))
	defer func() { _ = reader.Close() }()
	got, err := io.ReadAll(reader)
	if !errors.Is(err, encryption.ErrIntegrity) {
		t.Fatalf("streamed read of tampered tail = %v, want ErrIntegrity", err)
	}
	// Every byte served before the failure was frame-authenticated.
	if !bytes.Equal(got, body[:len(got)]) {
		t.Fatal("bytes served before tamper detection do not match the plaintext prefix")
	}
}
