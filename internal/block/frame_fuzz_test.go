package block_test

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"testing"

	"github.com/petabytecl/scrap/internal/block"
)

func FuzzReadFrame(f *testing.F) {
	var valid bytes.Buffer
	if err := block.WriteFrame(&valid, block.FrameHeader{
		Flags:    block.FlagSingleFrame,
		DocSeq:   3,
		FrameSeq: 0,
	}, []byte("valid frame payload")); err != nil {
		f.Fatalf("WriteFrame seed: %v", err)
	}

	f.Add(valid.Bytes())
	f.Add([]byte{})
	f.Add(valid.Bytes()[:block.FrameHeaderSize-1])
	badMagic := append([]byte(nil), valid.Bytes()...)
	badMagic[0] ^= 0xff
	updateFrameHeaderCRC(badMagic)
	f.Add(badMagic)
	badVersion := append([]byte(nil), valid.Bytes()...)
	badVersion[2]++
	updateFrameHeaderCRC(badVersion)
	f.Add(badVersion)
	oversized := append([]byte(nil), valid.Bytes()...)
	binary.LittleEndian.PutUint32(oversized[16:20], block.MaxFramePayload+1)
	updateFrameHeaderCRC(oversized)
	f.Add(oversized)
	badPayloadCRC := append([]byte(nil), valid.Bytes()...)
	badPayloadCRC[block.FrameHeaderSize] ^= 0xff
	f.Add(badPayloadCRC)
	badHeaderCRC := append([]byte(nil), valid.Bytes()...)
	badHeaderCRC[0] ^= 0xff
	f.Add(badHeaderCRC)

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > block.FrameHeaderSize+block.MaxFramePayload {
			return
		}

		hdr, payload, err := block.ReadFrame(bytes.NewReader(data))
		if err != nil {
			return
		}
		if len(payload) != int(hdr.PayloadLen) {
			t.Fatalf("payload length = %d, want %d", len(payload), hdr.PayloadLen)
		}

		var encoded bytes.Buffer
		if err := block.WriteFrame(&encoded, hdr, payload); err != nil {
			t.Fatalf("WriteFrame decoded value: %v", err)
		}
		roundHdr, roundPayload, err := block.ReadFrame(&encoded)
		if err != nil {
			t.Fatalf("ReadFrame round trip: %v", err)
		}
		if roundHdr != hdr {
			t.Fatalf("round-trip header = %+v, want %+v", roundHdr, hdr)
		}
		if !bytes.Equal(roundPayload, payload) {
			t.Fatalf("round-trip payload length = %d, want %d", len(roundPayload), len(payload))
		}
	})
}

func updateFrameHeaderCRC(frame []byte) {
	table := crc32.MakeTable(crc32.Castagnoli)
	binary.LittleEndian.PutUint32(frame[28:32], crc32.Checksum(frame[:28], table))
}
