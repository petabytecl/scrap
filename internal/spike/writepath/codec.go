package writepath

import (
	"bytes"
	"encoding/gob"

	"google.golang.org/grpc/encoding"
)

const codecName = "scrap-spike-gob"

func init() {
	encoding.RegisterCodec(gobCodec{})
}

type gobCodec struct{}

func (gobCodec) Name() string {
	return codecName
}

func (gobCodec) Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (gobCodec) Unmarshal(data []byte, v any) error {
	return gob.NewDecoder(bytes.NewReader(data)).Decode(v)
}
