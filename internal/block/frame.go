package block

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

const (
	FrameHeaderSize = 32
	MaxFramePayload = 64 * 1024

	frameMagic   = 0x5343 // "SC"
	frameVersion = 0x01
)

const (
	FlagFirstFrame  byte = 0x01
	FlagLastFrame   byte = 0x02
	FlagSingleFrame      = FlagFirstFrame | FlagLastFrame
)

var (
	ErrBadMagic     = errors.New("block: invalid frame magic")
	ErrBadVersion   = errors.New("block: unsupported frame version")
	ErrCRCMismatch  = errors.New("block: payload CRC-32C mismatch")
	ErrHeaderCRC    = errors.New("block: header CRC-32C mismatch")
	ErrTruncated    = errors.New("block: truncated frame")
	ErrPayloadLimit = errors.New("block: payload exceeds 64 KiB")

	crcTable = crc32.MakeTable(crc32.Castagnoli)
)

type FrameHeader struct {
	Flags      byte
	DocSeq     uint32
	FrameSeq   uint32
	PayloadLen uint32
	PayloadCRC uint32
}

// WriteFrame writes a 32-byte frame header and payload.
// Layout: magic(2) + version(1) + flags(1) + header_len(2) + reserved(2) +
//
//	doc_seq(4) + frame_seq(4) + payload_len(4) + payload_crc32c(4) +
//	reserved(4) + header_crc32c(4)
func WriteFrame(w io.Writer, hdr FrameHeader, payload []byte) error {
	if len(payload) > MaxFramePayload {
		return ErrPayloadLimit
	}

	var buf [FrameHeaderSize]byte
	binary.LittleEndian.PutUint16(buf[0:2], frameMagic)
	buf[2] = frameVersion
	buf[3] = hdr.Flags
	binary.LittleEndian.PutUint16(buf[4:6], FrameHeaderSize)
	// buf[6:8] reserved
	binary.LittleEndian.PutUint32(buf[8:12], hdr.DocSeq)
	binary.LittleEndian.PutUint32(buf[12:16], hdr.FrameSeq)
	binary.LittleEndian.PutUint32(buf[16:20], uint32(len(payload))) //nolint:gosec // payload bounded by MaxFramePayload (64 KiB) check above
	binary.LittleEndian.PutUint32(buf[20:24], crc32.Checksum(payload, crcTable))
	// buf[24:28] reserved
	binary.LittleEndian.PutUint32(buf[28:32], crc32.Checksum(buf[0:28], crcTable))

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

//nolint:cyclop // payloadLen bounds check is a necessary safety guard
func ReadFrame(r io.Reader) (FrameHeader, []byte, error) {
	var buf [FrameHeaderSize]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return FrameHeader{}, nil, ErrTruncated
		}
		return FrameHeader{}, nil, fmt.Errorf("block: read frame header: %w", err)
	}

	headerCRC := binary.LittleEndian.Uint32(buf[28:32])
	if crc32.Checksum(buf[0:28], crcTable) != headerCRC {
		return FrameHeader{}, nil, ErrHeaderCRC
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
		DocSeq:   binary.LittleEndian.Uint32(buf[8:12]),
		FrameSeq: binary.LittleEndian.Uint32(buf[12:16]),
	}

	payloadLen := binary.LittleEndian.Uint32(buf[16:20])
	if payloadLen > MaxFramePayload {
		return FrameHeader{}, nil, ErrPayloadLimit
	}
	payloadCRC := binary.LittleEndian.Uint32(buf[20:24])

	hdr.PayloadLen = payloadLen
	hdr.PayloadCRC = payloadCRC

	payload := make([]byte, payloadLen)
	if payloadLen > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return FrameHeader{}, nil, ErrTruncated
			}
			return FrameHeader{}, nil, fmt.Errorf("block: read frame payload: %w", err)
		}
	}

	if crc32.Checksum(payload, crcTable) != payloadCRC {
		return FrameHeader{}, nil, ErrCRCMismatch
	}

	return hdr, payload, nil
}
