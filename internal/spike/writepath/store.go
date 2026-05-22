package writepath

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	blockHeaderLength = 64
	defaultFrameSize  = 1024 * 1024
	defaultShardID    = 1
)

type Store struct {
	mu          sync.Mutex
	dir         string
	blocksDir   string
	openlogDir  string
	db          *pebble.DB
	blockID     string
	blockPath   string
	blockFile   *os.File
	blockOffset int64
	faults      StoreFaults
	peers       []PeerPreparer
	required    int
	barrier     MetadataCommitBarrier
}

type StoreOptions struct {
	Faults                StoreFaults
	RequiredPeerPrepares  int
	PeerPreparers         []PeerPreparer
	MetadataCommitBarrier MetadataCommitBarrier
}

type StoreFaults struct {
	AfterBlockSync      func(DocumentRecord) error
	AfterOpenLogSync    func(DocumentRecord) error
	AfterMetadataCommit func(DocumentRecord) error
}

type PeerPreparer interface {
	PrepareDocument(record DocumentRecord, reader io.Reader) error
}

type MetadataCommitBarrier interface {
	CommitDocument(context.Context, DocumentRecord) error
}

type immediateMetadataCommitBarrier struct{}

func (immediateMetadataCommitBarrier) CommitDocument(context.Context, DocumentRecord) error {
	return nil
}

func OpenStore(dir string) (*Store, error) {
	return OpenStoreWithOptions(dir, StoreOptions{})
}

func OpenStoreWithOptions(dir string, options StoreOptions) (*Store, error) {
	blocksDir := filepath.Join(dir, "blocks")
	openlogDir := filepath.Join(dir, "openlog")
	metadataDir := filepath.Join(dir, "metadata")

	for _, path := range []string{blocksDir, openlogDir, metadataDir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return nil, err
		}
	}

	db, err := pebble.Open(metadataDir, &pebble.Options{})
	if err != nil {
		return nil, err
	}

	blockID, err := newUUIDv7ish()
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	blockPath := filepath.Join(blocksDir, blockID+".blk")
	blockFile, err := os.OpenFile(blockPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := writeBlockHeader(blockFile, blockID); err != nil {
		_ = blockFile.Close()
		_ = db.Close()
		return nil, err
	}
	if err := blockFile.Sync(); err != nil {
		_ = blockFile.Close()
		_ = db.Close()
		return nil, err
	}
	if err := syncDir(blocksDir); err != nil {
		_ = blockFile.Close()
		_ = db.Close()
		return nil, err
	}
	barrier := options.MetadataCommitBarrier
	if barrier == nil {
		barrier = immediateMetadataCommitBarrier{}
	}

	return &Store{
		dir:         dir,
		blocksDir:   blocksDir,
		openlogDir:  openlogDir,
		db:          db,
		blockID:     blockID,
		blockPath:   blockPath,
		blockFile:   blockFile,
		blockOffset: blockHeaderLength,
		faults:      options.Faults,
		peers:       options.PeerPreparers,
		required:    options.RequiredPeerPrepares,
		barrier:     barrier,
	}, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var err error
	if s.blockFile != nil {
		err = errors.Join(err, s.blockFile.Close())
		s.blockFile = nil
	}
	if s.db != nil {
		err = errors.Join(err, s.db.Close())
		s.db = nil
	}
	return err
}

func (s *Store) WriteDocument(meta *WriteDocumentMetadata, nextChunk func() ([]byte, error)) (*WriteDocumentResult, error) {
	if meta == nil {
		return nil, status.Error(codes.InvalidArgument, "first write message must include metadata")
	}
	if meta.TenantID == "" || meta.TransactionID == "" || meta.DocumentName == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant_id, transaction_id, and document_name are required")
	}

	key := documentKey(meta.TenantID, meta.TransactionID, meta.DocumentName)
	if existing, ok, err := s.getDocument(key); err != nil {
		return nil, err
	} else if ok {
		return &WriteDocumentResult{
			TenantID:      existing.TenantID,
			TransactionID: existing.TransactionID,
			DocumentName:  existing.DocumentName,
			ByteLength:    existing.ByteLength,
			LogicalSHA256: existing.LogicalSHA256,
			BlockID:       existing.BlockID,
			StoredOffset:  existing.StoredOffset,
			StoredLength:  existing.StoredLength,
			AckedUnixNano: time.Now().UnixNano(),
		}, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	startOffset := s.blockOffset
	if _, err := s.blockFile.Seek(startOffset, io.SeekStart); err != nil {
		return nil, err
	}

	committed := false
	defer func() {
		if !committed {
			_ = s.blockFile.Truncate(startOffset)
			_, _ = s.blockFile.Seek(startOffset, io.SeekStart)
		}
	}()

	hasher := sha256.New()
	frameChecksums := newFrameAccumulator()
	var byteLength int64
	for {
		chunk, err := nextChunk()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(chunk) == 0 {
			continue
		}
		n, err := s.blockFile.Write(chunk)
		if err != nil {
			return nil, err
		}
		if n != len(chunk) {
			return nil, io.ErrShortWrite
		}
		if _, err := hasher.Write(chunk); err != nil {
			return nil, err
		}
		frameChecksums.Write(startOffset+byteLength, chunk)
		byteLength += int64(n)
	}

	logicalSHA := hex.EncodeToString(hasher.Sum(nil))
	if meta.ExpectedLength > 0 && meta.ExpectedLength != byteLength {
		return nil, status.Errorf(codes.FailedPrecondition, "expected length %d, got %d", meta.ExpectedLength, byteLength)
	}
	if meta.ExpectedSHA256Hex != "" && meta.ExpectedSHA256Hex != logicalSHA {
		return nil, status.Errorf(codes.FailedPrecondition, "expected sha256 %s, got %s", meta.ExpectedSHA256Hex, logicalSHA)
	}

	if err := s.blockFile.Sync(); err != nil {
		return nil, err
	}

	record := DocumentRecord{
		TenantID:      meta.TenantID,
		TransactionID: meta.TransactionID,
		DocumentName:  meta.DocumentName,
		ContentType:   meta.ContentType,
		ByteLength:    byteLength,
		LogicalSHA256: logicalSHA,
		BlockID:       s.blockID,
		StoredOffset:  startOffset,
		StoredLength:  byteLength,
		Frames:        frameChecksums.Records(),
		CommittedNano: time.Now().UnixNano(),
	}
	if s.faults.AfterBlockSync != nil {
		if err := s.faults.AfterBlockSync(record); err != nil {
			return nil, err
		}
	}
	if err := s.appendOpenLog(record); err != nil {
		return nil, err
	}
	if s.faults.AfterOpenLogSync != nil {
		if err := s.faults.AfterOpenLogSync(record); err != nil {
			return nil, err
		}
	}
	if err := s.preparePeers(record); err != nil {
		return nil, err
	}
	if err := s.barrier.CommitDocument(context.Background(), record); err != nil {
		return nil, err
	}
	if err := s.putDocument(key, record); err != nil {
		return nil, err
	}
	if s.faults.AfterMetadataCommit != nil {
		_ = s.faults.AfterMetadataCommit(record)
	}

	s.blockOffset = startOffset + byteLength
	committed = true

	return &WriteDocumentResult{
		TenantID:      record.TenantID,
		TransactionID: record.TransactionID,
		DocumentName:  record.DocumentName,
		ByteLength:    record.ByteLength,
		LogicalSHA256: record.LogicalSHA256,
		BlockID:       record.BlockID,
		StoredOffset:  record.StoredOffset,
		StoredLength:  record.StoredLength,
		AckedUnixNano: time.Now().UnixNano(),
	}, nil
}

func (s *Store) HeadDocument(req *HeadDocumentRequest) (*HeadDocumentResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "head request is required")
	}
	doc, ok, err := s.getDocument(documentKey(req.TenantID, req.TransactionID, req.DocumentName))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, status.Error(codes.NotFound, "document not found")
	}
	return &HeadDocumentResponse{Document: doc}, nil
}

func (s *Store) ReadDocument(req *ReadDocumentRequest, send func(*ReadDocumentResponse) error) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "read request is required")
	}
	doc, ok, err := s.getDocument(documentKey(req.TenantID, req.TransactionID, req.DocumentName))
	if err != nil {
		return err
	}
	if !ok {
		return status.Error(codes.NotFound, "document not found")
	}

	offset := req.Offset
	if offset < 0 || offset > doc.ByteLength {
		return status.Error(codes.InvalidArgument, "invalid read offset")
	}
	length := req.Length
	if length == 0 {
		length = doc.ByteLength - offset
	}
	if length < 0 || offset+length > doc.ByteLength {
		return status.Error(codes.InvalidArgument, "invalid read length")
	}

	if err := send(&ReadDocumentResponse{Document: &doc}); err != nil {
		return err
	}

	blockPath := filepath.Join(s.blocksDir, doc.BlockID+".blk")
	file, err := os.Open(blockPath)
	if err != nil {
		return status.Errorf(codes.DataLoss, "open block: %v", err)
	}
	defer file.Close()

	if err := verifyReadableRange(file, doc, offset, length); err != nil {
		return err
	}

	const readChunkSize = 1024 * 1024
	buf := make([]byte, readChunkSize)
	remaining := length
	readAt := doc.StoredOffset + offset
	for remaining > 0 {
		n := minInt64(int64(len(buf)), remaining)
		read, err := file.ReadAt(buf[:n], readAt)
		if err != nil {
			return status.Errorf(codes.DataLoss, "read block: %v", err)
		}
		if read == 0 {
			return status.Error(codes.DataLoss, "unexpected end of block")
		}
		if err := send(&ReadDocumentResponse{
			Offset: offset,
			Chunk:  append([]byte(nil), buf[:read]...),
		}); err != nil {
			return err
		}
		readAt += int64(read)
		offset += int64(read)
		remaining -= int64(read)
	}
	return nil
}

func verifyReadableRange(file *os.File, doc DocumentRecord, offset, length int64) error {
	if offset+length > doc.StoredLength {
		return status.Error(codes.DataLoss, "document range exceeds stored length")
	}
	info, err := file.Stat()
	if err != nil {
		return status.Errorf(codes.DataLoss, "stat block: %v", err)
	}
	if info.Size() < doc.StoredOffset+offset+length {
		return status.Error(codes.DataLoss, "block is shorter than committed document range")
	}
	if len(doc.Frames) == 0 {
		return verifyWholeDocument(file, doc)
	}

	rangeStart := doc.StoredOffset + offset
	rangeEnd := rangeStart + length
	coveredUntil := rangeStart
	for _, frame := range doc.Frames {
		segmentStart := frame.SegmentOffset
		segmentEnd := frame.SegmentOffset + frame.SegmentLength
		if segmentEnd <= rangeStart {
			continue
		}
		if segmentStart >= rangeEnd {
			break
		}
		if segmentStart > coveredUntil {
			return status.Error(codes.DataLoss, "document frame metadata has a gap")
		}
		if err := verifyFrameSegment(file, frame); err != nil {
			return err
		}
		if segmentEnd > coveredUntil {
			coveredUntil = segmentEnd
		}
		if coveredUntil >= rangeEnd {
			return nil
		}
	}
	return status.Error(codes.DataLoss, "document frame metadata does not cover requested range")
}

func verifyWholeDocument(file *os.File, doc DocumentRecord) error {
	hasher := sha256.New()
	reader := io.NewSectionReader(file, doc.StoredOffset, doc.StoredLength)
	if _, err := io.Copy(hasher, reader); err != nil {
		return status.Errorf(codes.DataLoss, "checksum block: %v", err)
	}
	if hex.EncodeToString(hasher.Sum(nil)) != doc.LogicalSHA256 {
		return status.Error(codes.DataLoss, "document checksum mismatch")
	}
	return nil
}

func verifyFrameSegment(file *os.File, frame FrameRecord) error {
	hasher := sha256.New()
	reader := io.NewSectionReader(file, frame.SegmentOffset, frame.SegmentLength)
	if _, err := io.Copy(hasher, reader); err != nil {
		return status.Errorf(codes.DataLoss, "checksum frame: %v", err)
	}
	if hex.EncodeToString(hasher.Sum(nil)) != frame.SHA256 {
		return status.Error(codes.DataLoss, "frame checksum mismatch")
	}
	return nil
}

func (s *Store) preparePeers(record DocumentRecord) error {
	if s.required == 0 {
		return nil
	}
	if len(s.peers) < s.required {
		return status.Errorf(codes.FailedPrecondition, "required %d peer prepares, got %d", s.required, len(s.peers))
	}
	for i := 0; i < s.required; i++ {
		file, err := os.Open(s.blockPath)
		if err != nil {
			return err
		}
		reader := io.NewSectionReader(file, record.StoredOffset, record.StoredLength)
		err = s.peers[i].PrepareDocument(record, reader)
		err = errors.Join(err, file.Close())
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) appendOpenLog(record DocumentRecord) error {
	path := filepath.Join(s.openlogDir, s.blockID+".openlog")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(file)
	if err := enc.Encode(struct {
		Type     string
		Document DocumentRecord
	}{
		Type:     "DocumentPrepared",
		Document: record,
	}); err != nil {
		_ = file.Close()
		return err
	}
	err = errors.Join(file.Sync(), file.Close(), syncDir(s.openlogDir))
	return err
}

func (s *Store) putDocument(key []byte, record DocumentRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return s.db.Set(key, data, pebble.Sync)
}

func (s *Store) getDocument(key []byte) (DocumentRecord, bool, error) {
	value, closer, err := s.db.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return DocumentRecord{}, false, nil
	}
	if err != nil {
		return DocumentRecord{}, false, err
	}
	defer closer.Close()

	var record DocumentRecord
	if err := json.Unmarshal(value, &record); err != nil {
		return DocumentRecord{}, false, err
	}
	return record, true, nil
}

func documentKey(tenantID, transactionID, documentName string) []byte {
	return []byte("doc\x00" + tenantID + "\x00" + transactionID + "\x00" + documentName)
}

func writeBlockHeader(file *os.File, blockID string) error {
	header := make([]byte, blockHeaderLength)
	copy(header[0:8], "SCRAPBLK")
	binary.BigEndian.PutUint16(header[8:10], 0)
	binary.BigEndian.PutUint16(header[10:12], 1)
	binary.BigEndian.PutUint32(header[12:16], blockHeaderLength)
	blockBytes, err := hex.DecodeString(stripUUIDDashes(blockID))
	if err != nil {
		return err
	}
	copy(header[16:32], blockBytes)
	binary.BigEndian.PutUint32(header[32:36], defaultShardID)
	binary.BigEndian.PutUint32(header[40:44], defaultFrameSize)
	if _, err := file.WriteAt(header, 0); err != nil {
		return err
	}
	_, err = file.Seek(blockHeaderLength, io.SeekStart)
	return err
}

func newUUIDv7ish() (string, error) {
	var b [16]byte
	now := uint64(time.Now().UnixMilli())
	b[0] = byte(now >> 40)
	b[1] = byte(now >> 32)
	b[2] = byte(now >> 24)
	b[3] = byte(now >> 16)
	b[4] = byte(now >> 8)
	b[5] = byte(now)
	if _, err := rand.Read(b[6:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func stripUUIDDashes(id string) string {
	out := make([]byte, 0, 32)
	for i := 0; i < len(id); i++ {
		if id[i] != '-' {
			out = append(out, id[i])
		}
	}
	return string(out)
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

type frameAccumulator struct {
	frames []pendingFrame
}

type pendingFrame struct {
	frameOffset   int64
	segmentOffset int64
	segmentLength int64
	hasher        hash.Hash
}

func newFrameAccumulator() *frameAccumulator {
	return &frameAccumulator{}
}

func (a *frameAccumulator) Write(offset int64, data []byte) {
	for len(data) > 0 {
		frameOffset := frameStart(offset)
		frameEnd := frameOffset + defaultFrameSize
		n := int(frameEnd - offset)
		if n > len(data) {
			n = len(data)
		}
		frame := a.current(frameOffset, offset)
		_, _ = frame.hasher.Write(data[:n])
		frame.segmentLength += int64(n)
		offset += int64(n)
		data = data[n:]
	}
}

func (a *frameAccumulator) current(frameOffset, segmentOffset int64) *pendingFrame {
	if len(a.frames) > 0 && a.frames[len(a.frames)-1].frameOffset == frameOffset {
		return &a.frames[len(a.frames)-1]
	}
	a.frames = append(a.frames, pendingFrame{
		frameOffset:   frameOffset,
		segmentOffset: segmentOffset,
		hasher:        sha256.New(),
	})
	return &a.frames[len(a.frames)-1]
}

func (a *frameAccumulator) Records() []FrameRecord {
	records := make([]FrameRecord, 0, len(a.frames))
	for _, frame := range a.frames {
		records = append(records, FrameRecord{
			FrameOffset:   frame.frameOffset,
			SegmentOffset: frame.segmentOffset,
			SegmentLength: frame.segmentLength,
			SHA256:        hex.EncodeToString(frame.hasher.Sum(nil)),
		})
	}
	return records
}

func frameStart(offset int64) int64 {
	return blockHeaderLength + ((offset - blockHeaderLength) / defaultFrameSize * defaultFrameSize)
}
