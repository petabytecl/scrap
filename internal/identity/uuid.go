package identity

import (
	"crypto/rand"
	"fmt"
	"strings"
	"time"
)

func NewUUIDv7() (string, error) {
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

func IsUUIDv7(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		case 14:
			if r != '7' {
				return false
			}
		case 19:
			if !strings.ContainsRune("89abAB", r) {
				return false
			}
		default:
			if !isHex(r) {
				return false
			}
		}
	}
	return true
}

func UUIDBytes(value string) ([16]byte, error) {
	var out [16]byte
	var compact [32]byte
	var n int
	for i := 0; i < len(value); i++ {
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
