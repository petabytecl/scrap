package block

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"os"
)

type CorruptionType string

const (
	CorruptionFrameCRC            CorruptionType = "frame_crc"
	CorruptionDocSHA256           CorruptionType = "doc_sha256"
	CorruptionDocCiphertextLength CorruptionType = "doc_ciphertext_length"
	CorruptionMissing             CorruptionType = "missing_frame"
)

type CorruptFrame struct {
	Offset int64
	Type   CorruptionType
}

type VerifyResult struct {
	CorruptFrames  []CorruptFrame
	FramesVerified uint64
}

type docAccumulator struct {
	hash         hash.Hash
	payloadBytes int64
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
	docBySeq := make(map[uint32]*docAccumulator)
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
		acc := accumulateDoc(docBySeq, hdr.DocSeq, payload)

		if isLastFrame(hdr.Flags) {
			completedDocSeq[hdr.DocSeq] = true
			checkDocIntegrity(acc, hdr.DocSeq, idxEntries, offset, &result)
			delete(docBySeq, hdr.DocSeq)
		}
		offset += int64(FrameHeaderSize) + int64(hdr.PayloadLen)
	}
	if reachedEOF {
		recordMissingIndexedFrames(idxEntries, framesByDocSeq, completedDocSeq, offset, &result)
	}
	return result
}

func accumulateDoc(accum map[uint32]*docAccumulator, docSeq uint32, payload []byte) *docAccumulator {
	acc, ok := accum[docSeq]
	if !ok {
		acc = &docAccumulator{hash: sha256.New()}
		accum[docSeq] = acc
	}
	acc.hash.Write(payload)
	acc.payloadBytes += int64(len(payload))
	return acc
}

func isLastFrame(flags byte) bool {
	return flags == FlagLastFrame || flags == FlagSingleFrame
}

func checkDocIntegrity(acc *docAccumulator, docSeq uint32, entries []IndexEntry, offset int64, result *VerifyResult) {
	idx, found := findIdxEntryByDocSeq(entries, docSeq)
	if !found {
		return
	}
	if len(idx.EncryptionEnvelope) > 0 {
		checkEncryptedDocPayloadLength(acc.payloadBytes, idx.EncryptionEnvelope, offset, result)
		return
	}
	checkDocSHA(acc.hash, idx.SHA256, offset, result)
}

func checkDocSHA(h hash.Hash, expected [32]byte, offset int64, result *VerifyResult) {
	var computed [32]byte
	copy(computed[:], h.Sum(nil))
	if isZeroSHA256(expected) || computed != expected {
		result.CorruptFrames = append(result.CorruptFrames, CorruptFrame{Offset: offset, Type: CorruptionDocSHA256})
	}
}

func checkEncryptedDocPayloadLength(payloadBytes int64, envelope []byte, offset int64, result *VerifyResult) {
	ciphertextLength, err := encryptedEnvelopeCiphertextLength(envelope)
	if err != nil || payloadBytes != ciphertextLength {
		result.CorruptFrames = append(result.CorruptFrames, CorruptFrame{Offset: offset, Type: CorruptionDocCiphertextLength})
	}
}

func encryptedEnvelopeCiphertextLength(envelope []byte) (int64, error) {
	var meta struct {
		CiphertextLength *int64 `json:"ciphertext_length"`
	}
	if err := json.Unmarshal(envelope, &meta); err != nil {
		return 0, err
	}
	if meta.CiphertextLength == nil || *meta.CiphertextLength < 0 {
		return 0, ErrIdxCorrupt
	}
	return *meta.CiphertextLength, nil
}

// docSeq is 0-indexed and matches the entry position in a well-formed block index.
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

	hdr, err := parseRawFrameHeader(buf)
	if err != nil {
		return FrameHeader{}, nil, err
	}

	payload, err := readRawFramePayload(r, hdr.PayloadLen)
	if err != nil {
		return FrameHeader{}, nil, err
	}
	if crc32.Checksum(payload, crcTable) != hdr.PayloadCRC {
		return FrameHeader{}, nil, ErrCRCMismatch
	}

	return hdr, payload, nil
}

func parseRawFrameHeader(buf [FrameHeaderSize]byte) (FrameHeader, error) {
	headerCRC := binary.LittleEndian.Uint32(buf[28:32])
	if crc32.Checksum(buf[0:28], crcTable) != headerCRC {
		return FrameHeader{}, ErrHeaderCRC
	}
	if magic := binary.LittleEndian.Uint16(buf[0:2]); magic != frameMagic {
		return FrameHeader{}, ErrBadMagic
	}
	if version := buf[2]; version != frameVersion {
		return FrameHeader{}, ErrBadVersion
	}

	hdr := FrameHeader{
		Flags:    buf[3],
		DocSeq:   binary.LittleEndian.Uint32(buf[8:12]),
		FrameSeq: binary.LittleEndian.Uint32(buf[12:16]),
	}
	payloadLen := binary.LittleEndian.Uint32(buf[16:20])
	if payloadLen > MaxFramePayload {
		return FrameHeader{}, ErrPayloadLimit
	}
	payloadCRC := binary.LittleEndian.Uint32(buf[20:24])
	hdr.PayloadLen = payloadLen
	hdr.PayloadCRC = payloadCRC
	return hdr, nil
}

func readRawFramePayload(r io.Reader, payloadLen uint32) ([]byte, error) {
	payload := make([]byte, payloadLen)
	if payloadLen > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, ErrCRCMismatch
		}
	}
	return payload, nil
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
