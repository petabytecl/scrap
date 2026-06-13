package block_test

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"testing"

	"github.com/petabytecl/scrap/internal/block"
)

func TestFrameRoundTrip(t *testing.T) {
	payload := []byte("hello, scrap")

	var buf bytes.Buffer
	err := block.WriteFrame(&buf, block.FrameHeader{
		DocSeq:   1,
		FrameSeq: 0,
		Flags:    block.FlagSingleFrame,
	}, payload)
	if err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	hdr, got, err := block.ReadFrame(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %q, want %q", got, payload)
	}
	if hdr.DocSeq != 1 {
		t.Fatalf("DocSeq: got %d, want 1", hdr.DocSeq)
	}
	if hdr.FrameSeq != 0 {
		t.Fatalf("FrameSeq: got %d, want 0", hdr.FrameSeq)
	}
	if hdr.Flags != block.FlagSingleFrame {
		t.Fatalf("Flags: got %d, want %d", hdr.Flags, block.FlagSingleFrame)
	}
}

func TestFrameMaxPayload(t *testing.T) {
	payload := make([]byte, block.MaxFramePayload)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	var buf bytes.Buffer
	err := block.WriteFrame(&buf, block.FrameHeader{
		DocSeq:   0,
		FrameSeq: 0,
		Flags:    block.FlagSingleFrame,
	}, payload)
	if err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	_, got, err := block.ReadFrame(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("64 KiB payload round-trip mismatch")
	}
}

func TestFrameCorruptPayload(t *testing.T) {
	payload := []byte("valid data")

	var buf bytes.Buffer
	_ = block.WriteFrame(&buf, block.FrameHeader{Flags: block.FlagSingleFrame}, payload)

	data := buf.Bytes()
	data[block.FrameHeaderSize+2] ^= 0xFF

	_, _, err := block.ReadFrame(bytes.NewReader(data))
	if err == nil {
		t.Fatal("expected CRC error on corrupt payload")
	}
}

func TestFrameCorruptHeaderCRC(t *testing.T) {
	payload := []byte("some bytes")

	var buf bytes.Buffer
	_ = block.WriteFrame(&buf, block.FrameHeader{Flags: block.FlagSingleFrame}, payload)

	data := buf.Bytes()
	data[3] ^= 0xFF // corrupt flags byte in header

	_, _, err := block.ReadFrame(bytes.NewReader(data))
	if err == nil {
		t.Fatal("expected header CRC error")
	}
}

func TestFrameTruncated(t *testing.T) {
	payload := []byte("truncated")

	var buf bytes.Buffer
	_ = block.WriteFrame(&buf, block.FrameHeader{Flags: block.FlagSingleFrame}, payload)

	truncated := buf.Bytes()[:block.FrameHeaderSize+3]
	_, _, err := block.ReadFrame(bytes.NewReader(truncated))
	if err == nil {
		t.Fatal("expected error on truncated frame")
	}
}

func TestFrameEmptyPayload(t *testing.T) {
	var buf bytes.Buffer
	err := block.WriteFrame(&buf, block.FrameHeader{Flags: block.FlagSingleFrame}, []byte{})
	if err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	_, got, err := block.ReadFrame(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty payload, got %d bytes", len(got))
	}
}

func TestFrameHeaderSize(t *testing.T) {
	if block.FrameHeaderSize != 32 {
		t.Fatalf("FrameHeaderSize: got %d, want 32", block.FrameHeaderSize)
	}
}

func TestFrameCRC32CCastagnoli(t *testing.T) {
	tab := crc32.MakeTable(crc32.Castagnoli)
	data := []byte("test crc")
	want := crc32.Checksum(data, tab)

	var buf bytes.Buffer
	_ = block.WriteFrame(&buf, block.FrameHeader{Flags: block.FlagSingleFrame}, data)

	raw := buf.Bytes()
	gotCRC := binary.LittleEndian.Uint32(raw[20:24])
	if gotCRC != want {
		t.Fatalf("payload CRC-32C mismatch: got %08x, want %08x", gotCRC, want)
	}
}
