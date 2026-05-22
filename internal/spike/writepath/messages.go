package writepath

type WriteDocumentMetadata struct {
	TenantID             string
	TransactionID        string
	DocumentName         string
	ContentType          string
	ExpectedLength       int64
	ExpectedSHA256Hex    string
	ClientIdempotencyKey string
}

type WriteDocumentRequest struct {
	Metadata *WriteDocumentMetadata
	Chunk    []byte
}

type WriteDocumentResult struct {
	TenantID      string
	TransactionID string
	DocumentName  string
	ByteLength    int64
	LogicalSHA256 string
	BlockID       string
	StoredOffset  int64
	StoredLength  int64
	AckedUnixNano int64
}

type HeadDocumentRequest struct {
	TenantID      string
	TransactionID string
	DocumentName  string
}

type HeadDocumentResponse struct {
	Document DocumentRecord
}

type ReadDocumentRequest struct {
	TenantID      string
	TransactionID string
	DocumentName  string
	Offset        int64
	Length        int64
}

type ReadDocumentResponse struct {
	Document *DocumentRecord
	Offset   int64
	Chunk    []byte
}

type DocumentRecord struct {
	TenantID      string
	TransactionID string
	DocumentName  string
	ContentType   string
	ByteLength    int64
	LogicalSHA256 string
	BlockID       string
	StoredOffset  int64
	StoredLength  int64
	Frames        []FrameRecord
	CommittedNano int64
}

type FrameRecord struct {
	FrameOffset   int64
	SegmentOffset int64
	SegmentLength int64
	SHA256        string
}
