package identity

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

const uuidTextLength = 36

func NewUUIDv7() (string, error) {
	var b [16]byte
	now := uint64(time.Now().UnixMilli())
	var timestamp [8]byte
	binary.BigEndian.PutUint64(timestamp[:], now)
	copy(b[0:6], timestamp[2:8])
	if _, err := rand.Read(b[6:]); err != nil {
		return "", fmt.Errorf("generate UUIDv7 randomness: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func IsUUIDv7(value string) bool {
	if len(value) != uuidTextLength {
		return false
	}
	for i, r := range value {
		if !isUUIDv7Rune(i, r) {
			return false
		}
	}
	return true
}

func isUUIDv7Rune(index int, r rune) bool {
	switch index {
	case 8, 13, 18, 23:
		return r == '-'
	case 14:
		return r == '7'
	case 19:
		return strings.ContainsRune("89abAB", r)
	default:
		return isHex(r)
	}
}

func UUIDBytes(value string) ([16]byte, error) {
	var out [16]byte
	var compact [32]byte
	var n int
	for i := range len(value) {
		if value[i] == '-' {
			continue
		}
		if n >= len(compact) {
			return out, fmt.Errorf("uuid has too many hex bytes: %q", value)
		}
		compact[n] = value[i]
		n++
	}
	if n != len(compact) {
		return out, fmt.Errorf("uuid has %d hex bytes, want 32", n)
	}
	for i := 0; i < len(out); i++ {
		hi, ok := hexValue(compact[i*2])
		if !ok {
			return out, fmt.Errorf("uuid contains non-hex byte: %q", value)
		}
		lo, ok := hexValue(compact[i*2+1])
		if !ok {
			return out, fmt.Errorf("uuid contains non-hex byte: %q", value)
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

func isHex(r rune) bool {
	return ('0' <= r && r <= '9') || ('a' <= r && r <= 'f') || ('A' <= r && r <= 'F')
}

func hexValue(b byte) (byte, bool) {
	switch {
	case '0' <= b && b <= '9':
		return b - '0', true
	case 'a' <= b && b <= 'f':
		return b - 'a' + 10, true
	case 'A' <= b && b <= 'F':
		return b - 'A' + 10, true
	default:
		return 0, false
	}
}
