package block

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

const (
	FrameHeaderSize = 18
	MaxFramePayload = 64 * 1024

	frameMagic   = 0x5343 // "SC"
	frameVersion = 0x01
)

const (
	FlagFirstFrame  byte = 0x01
	FlagLastFrame   byte = 0x02
	FlagSingleFrame byte = FlagFirstFrame | FlagLastFrame
)

var (
	ErrBadMagic     = errors.New("block: invalid frame magic")
	ErrBadVersion   = errors.New("block: unsupported frame version")
	ErrCRCMismatch  = errors.New("block: CRC-32C mismatch")
	ErrTruncated    = errors.New("block: truncated frame")
	ErrPayloadLimit = errors.New("block: payload exceeds 64 KiB")

	crcTable = crc32.MakeTable(crc32.Castagnoli)
)

type FrameHeader struct {
	DocSeq   uint32
	FrameSeq uint16
	Flags    byte
}

// WriteFrame writes a frame header + payload to w.
// Layout (18 bytes): magic(2) + version(1) + flags(1) + doc_seq(4) + frame_seq(2) + payload_len(4) + crc32c(4)
func WriteFrame(w io.Writer, hdr FrameHeader, payload []byte) error {
	if len(payload) > MaxFramePayload {
		return ErrPayloadLimit
	}

	var buf [FrameHeaderSize]byte
	binary.LittleEndian.PutUint16(buf[0:2], frameMagic)
	buf[2] = frameVersion
	buf[3] = hdr.Flags
	binary.LittleEndian.PutUint32(buf[4:8], hdr.DocSeq)
	binary.LittleEndian.PutUint16(buf[8:10], hdr.FrameSeq)
	binary.LittleEndian.PutUint32(buf[10:14], uint32(len(payload)))
	binary.LittleEndian.PutUint32(buf[14:18], crc32.Checksum(payload, crcTable))

	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("block: write frame header: %w", err)
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return fmt.Errorf("block: write frame payload: %w", err)
		}
	}
	return nil
}

// ReadFrame reads a single frame from r, verifying magic, version, and CRC-32C.
func ReadFrame(r io.Reader) (FrameHeader, []byte, error) {
	var buf [FrameHeaderSize]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return FrameHeader{}, nil, ErrTruncated
		}
		return FrameHeader{}, nil, fmt.Errorf("block: read frame header: %w", err)
	}

	magic := binary.LittleEndian.Uint16(buf[0:2])
	if magic != frameMagic {
		return FrameHeader{}, nil, ErrBadMagic
	}

	version := buf[2]
	if version != frameVersion {
		return FrameHeader{}, nil, ErrBadVersion
	}

	hdr := FrameHeader{
		Flags:    buf[3],
		DocSeq:   binary.LittleEndian.Uint32(buf[4:8]),
		FrameSeq: binary.LittleEndian.Uint16(buf[8:10]),
	}

	payloadLen := binary.LittleEndian.Uint32(buf[10:14])
	expectedCRC := binary.LittleEndian.Uint32(buf[14:18])

	payload := make([]byte, payloadLen)
	if payloadLen > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return FrameHeader{}, nil, ErrTruncated
			}
			return FrameHeader{}, nil, fmt.Errorf("block: read frame payload: %w", err)
		}
	}

	if crc32.Checksum(payload, crcTable) != expectedCRC {
		return FrameHeader{}, nil, ErrCRCMismatch
	}

	return hdr, payload, nil
}
