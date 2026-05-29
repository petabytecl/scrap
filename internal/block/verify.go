package block

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"os"
)

type CorruptionType string

const (
	CorruptionFrameCRC  CorruptionType = "frame_crc"
	CorruptionDocSHA256 CorruptionType = "doc_sha256"
	CorruptionMissing   CorruptionType = "missing_frame"
)

type CorruptFrame struct {
	Offset int64
	Type   CorruptionType
}

type VerifyResult struct {
	CorruptFrames  []CorruptFrame
	FramesVerified uint64
}

func VerifyBlock(blkPath, idxPath string) (VerifyResult, error) {
	idxEntries, err := loadIdxEntries(idxPath)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("block: verify load idx: %w", err)
	}

	f, err := os.Open(blkPath) //nolint:gosec // path constructed by caller from controlled block IDs
	if err != nil {
		return VerifyResult{}, fmt.Errorf("block: verify open: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Seek(HeaderSize, io.SeekStart); err != nil {
		return VerifyResult{}, fmt.Errorf("block: verify seek past header: %w", err)
	}

	return verifyFrames(f, idxEntries), nil
}

func verifyFrames(f io.Reader, idxEntries []IndexEntry) VerifyResult {
	var result VerifyResult
	offset := int64(HeaderSize)
	docBySeq := make(map[uint32]hash.Hash)
	framesByDocSeq := make(map[uint32]uint32)
	completedDocSeq := make(map[uint32]bool)
	reachedEOF := false

	for {
		hdr, payload, readErr := readFrameRaw(f)
		if errors.Is(readErr, io.EOF) {
			reachedEOF = true
			break
		}
		if readErr != nil {
			result.CorruptFrames = append(result.CorruptFrames, CorruptFrame{Offset: offset, Type: CorruptionFrameCRC})
			break
		}

		result.FramesVerified++
		framesByDocSeq[hdr.DocSeq]++
		h := accumulateDoc(docBySeq, hdr.DocSeq, payload)

		if isLastFrame(hdr.Flags) {
			completedDocSeq[hdr.DocSeq] = true
			checkDocSHA(h, hdr.DocSeq, idxEntries, offset, &result)
			delete(docBySeq, hdr.DocSeq)
		}
		offset += int64(FrameHeaderSize) + int64(hdr.PayloadLen)
	}
	if reachedEOF {
		recordMissingIndexedFrames(idxEntries, framesByDocSeq, completedDocSeq, offset, &result)
	}
	return result
}

func accumulateDoc(accum map[uint32]hash.Hash, docSeq uint32, payload []byte) hash.Hash {
	h, ok := accum[docSeq]
	if !ok {
		h = sha256.New()
		accum[docSeq] = h
	}
	h.Write(payload)
	return h
}

func isLastFrame(flags byte) bool {
	return flags == FlagLastFrame || flags == FlagSingleFrame
}

func checkDocSHA(h hash.Hash, docSeq uint32, entries []IndexEntry, offset int64, result *VerifyResult) {
	var computed [32]byte
	copy(computed[:], h.Sum(nil))
	idx, found := findIdxEntryByDocSeq(entries, docSeq)
	if !found {
		return
	}
	var empty [32]byte
	if idx.SHA256 != empty && computed != idx.SHA256 {
		result.CorruptFrames = append(result.CorruptFrames, CorruptFrame{Offset: offset, Type: CorruptionDocSHA256})
	}
}

// docSeq is 0-indexed and matches entry position in a well-formed block index.
func recordMissingIndexedFrames(entries []IndexEntry, framesByDocSeq map[uint32]uint32, completedDocSeq map[uint32]bool, offset int64, result *VerifyResult) {
	for docSeq, entry := range entries {
		if entry.FrameCount == 0 {
			continue
		}
		seq := uint32(docSeq)
		if framesByDocSeq[seq] < entry.FrameCount || !completedDocSeq[seq] {
			result.CorruptFrames = append(result.CorruptFrames, CorruptFrame{Offset: offset, Type: CorruptionMissing})
		}
	}
}

func readFrameRaw(r io.Reader) (FrameHeader, []byte, error) {
	var buf [FrameHeaderSize]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return FrameHeader{}, nil, io.EOF
		}
		return FrameHeader{}, nil, fmt.Errorf("block: verify read header: %w", err)
	}

	headerCRC := binary.LittleEndian.Uint32(buf[28:32])
	if crc32.Checksum(buf[0:28], crcTable) != headerCRC {
		return FrameHeader{}, nil, ErrHeaderCRC
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
			return FrameHeader{}, nil, ErrCRCMismatch
		}
	}

	if crc32.Checksum(payload, crcTable) != payloadCRC {
		return FrameHeader{}, nil, ErrCRCMismatch
	}

	return hdr, payload, nil
}

func loadIdxEntries(idxPath string) ([]IndexEntry, error) {
	ir, err := OpenIndexReader(idxPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = ir.Close() }()
	return ir.Entries(), nil
}

func findIdxEntryByDocSeq(entries []IndexEntry, docSeq uint32) (IndexEntry, bool) {
	if int(docSeq) < len(entries) {
		return entries[docSeq], true
	}
	return IndexEntry{}, false
}

func RecomputeFramePayloadCRC(data []byte, frameStart int) {
	payloadLen := binary.LittleEndian.Uint32(data[frameStart+16 : frameStart+20])
	payload := data[frameStart+FrameHeaderSize : frameStart+FrameHeaderSize+int(payloadLen)]
	newCRC := crc32.Checksum(payload, crcTable)
	binary.LittleEndian.PutUint32(data[frameStart+20:frameStart+24], newCRC)
	headerCRC := crc32.Checksum(data[frameStart:frameStart+28], crcTable)
	binary.LittleEndian.PutUint32(data[frameStart+28:frameStart+32], headerCRC)
}
