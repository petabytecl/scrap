package ulid

import (
	"crypto/rand"
	"errors"
	"sync"
	"time"
)

const (
	ulidSize      = 16
	encodedLength = 26
	invalidByte   = 0xFF
)

type ULID [ulidSize]byte

func (u ULID) Time() uint64 {
	return uint64(u[0])<<40 | uint64(u[1])<<32 |
		uint64(u[2])<<24 | uint64(u[3])<<16 |
		uint64(u[4])<<8 | uint64(u[5])
}

func (u ULID) IsZero() bool {
	return u == ULID{}
}

func (u ULID) Bytes() []byte {
	b := make([]byte, ulidSize)
	copy(b, u[:])
	return b
}

func (u ULID) MarshalText() ([]byte, error) {
	return []byte(u.String()), nil
}

func (u *ULID) UnmarshalText(data []byte) error {
	parsed, err := Parse(string(data))
	if err != nil {
		return err
	}
	*u = parsed
	return nil
}

func (u ULID) MarshalBinary() ([]byte, error) {
	return u.Bytes(), nil
}

func (u *ULID) UnmarshalBinary(data []byte) error {
	if len(data) != ulidSize {
		return errors.New("ulid: invalid binary length")
	}
	copy(u[:], data)
	return nil
}

func (u ULID) Compare(other ULID) int {
	for i := range u {
		if u[i] < other[i] {
			return -1
		}
		if u[i] > other[i] {
			return 1
		}
	}
	return 0
}

const encodingChars = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var decodeTable [256]byte

func init() {
	for i := range decodeTable {
		decodeTable[i] = invalidByte
	}
	for i, c := range encodingChars {
		decodeTable[c] = byte(i) //nolint:gosec // i is bounded by len(encodingChars)=32, fits in byte
		if c >= 'A' && c <= 'Z' {
			decodeTable[c+32] = byte(i) //nolint:gosec // same bound
		}
	}
}

//nolint:mnd // Crockford Base32 bitmask constants are the encoding algorithm
func (u ULID) String() string {
	var buf [26]byte
	buf[0] = encodingChars[(u[0]&224)>>5]
	buf[1] = encodingChars[u[0]&31]
	buf[2] = encodingChars[(u[1]&248)>>3]
	buf[3] = encodingChars[((u[1]&7)<<2)|((u[2]&192)>>6)]
	buf[4] = encodingChars[(u[2]&62)>>1]
	buf[5] = encodingChars[((u[2]&1)<<4)|((u[3]&240)>>4)]
	buf[6] = encodingChars[((u[3]&15)<<1)|((u[4]&128)>>7)]
	buf[7] = encodingChars[(u[4]&124)>>2]
	buf[8] = encodingChars[((u[4]&3)<<3)|((u[5]&224)>>5)]
	buf[9] = encodingChars[u[5]&31]

	buf[10] = encodingChars[(u[6]&248)>>3]
	buf[11] = encodingChars[((u[6]&7)<<2)|((u[7]&192)>>6)]
	buf[12] = encodingChars[(u[7]&62)>>1]
	buf[13] = encodingChars[((u[7]&1)<<4)|((u[8]&240)>>4)]
	buf[14] = encodingChars[((u[8]&15)<<1)|((u[9]&128)>>7)]
	buf[15] = encodingChars[(u[9]&124)>>2]
	buf[16] = encodingChars[((u[9]&3)<<3)|((u[10]&224)>>5)]
	buf[17] = encodingChars[u[10]&31]
	buf[18] = encodingChars[(u[11]&248)>>3]
	buf[19] = encodingChars[((u[11]&7)<<2)|((u[12]&192)>>6)]
	buf[20] = encodingChars[(u[12]&62)>>1]
	buf[21] = encodingChars[((u[12]&1)<<4)|((u[13]&240)>>4)]
	buf[22] = encodingChars[((u[13]&15)<<1)|((u[14]&128)>>7)]
	buf[23] = encodingChars[(u[14]&124)>>2]
	buf[24] = encodingChars[((u[14]&3)<<3)|((u[15]&224)>>5)]
	buf[25] = encodingChars[u[15]&31]

	return string(buf[:])
}

//nolint:mnd // Crockford Base32 bit-shift constants are the decoding algorithm
func Parse(s string) (ULID, error) {
	if len(s) != encodedLength {
		return ULID{}, errors.New("ulid: invalid length")
	}

	var u ULID
	var d [encodedLength]byte
	for i := range encodedLength {
		v := decodeTable[s[i]]
		if v == invalidByte {
			return ULID{}, errors.New("ulid: invalid character")
		}
		d[i] = v
	}

	u[0] = (d[0] << 5) | d[1]
	u[1] = (d[2] << 3) | (d[3] >> 2)
	u[2] = (d[3] << 6) | (d[4] << 1) | (d[5] >> 4)
	u[3] = (d[5] << 4) | (d[6] >> 1)
	u[4] = (d[6] << 7) | (d[7] << 2) | (d[8] >> 3)
	u[5] = (d[8] << 5) | d[9]

	u[6] = (d[10] << 3) | (d[11] >> 2)
	u[7] = (d[11] << 6) | (d[12] << 1) | (d[13] >> 4)
	u[8] = (d[13] << 4) | (d[14] >> 1)
	u[9] = (d[14] << 7) | (d[15] << 2) | (d[16] >> 3)
	u[10] = (d[16] << 5) | d[17]
	u[11] = (d[18] << 3) | (d[19] >> 2)
	u[12] = (d[19] << 6) | (d[20] << 1) | (d[21] >> 4)
	u[13] = (d[21] << 4) | (d[22] >> 1)
	u[14] = (d[22] << 7) | (d[23] << 2) | (d[24] >> 3)
	u[15] = (d[24] << 5) | d[25]

	return u, nil
}

type Option func(*Generator)

type Generator struct {
	mu         sync.Mutex
	clock      func() uint64
	lastTime   uint64
	lastRandom [10]byte
}

func WithClock(fn func() uint64) Option {
	return func(g *Generator) {
		g.clock = fn
	}
}

func NewGenerator(opts ...Option) *Generator {
	g := &Generator{
		clock: func() uint64 { return uint64(time.Now().UnixMilli()) },
	}
	for _, o := range opts {
		o(g)
	}
	return g
}

func (g *Generator) New() ULID {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.clock()

	if now == g.lastTime {
		g.incrementRandom()
	} else {
		if now < g.lastTime {
			now = g.lastTime
			g.incrementRandom()
		} else {
			g.lastTime = now
			g.fillRandom()
		}
	}

	var id ULID
	id[0] = byte(now >> 40) //nolint:gosec,mnd // intentional truncation for 48-bit big-endian timestamp
	id[1] = byte(now >> 32) //nolint:gosec,mnd // same
	id[2] = byte(now >> 24) //nolint:gosec,mnd // same
	id[3] = byte(now >> 16) //nolint:gosec,mnd // same
	id[4] = byte(now >> 8)  //nolint:gosec,mnd // same
	id[5] = byte(now)       //nolint:gosec // same
	copy(id[6:], g.lastRandom[:])
	return id
}

func (g *Generator) incrementRandom() {
	for i := 9; i >= 0; i-- {
		g.lastRandom[i]++
		if g.lastRandom[i] != 0 {
			return
		}
	}
	// Overflow: all 80 bits wrapped. Sleep until next ms.
	for {
		now := g.clock()
		if now > g.lastTime {
			g.lastTime = now
			g.fillRandom()
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func (g *Generator) fillRandom() {
	if _, err := rand.Read(g.lastRandom[:]); err != nil {
		panic("ulid: crypto/rand failed: " + err.Error())
	}
}

var defaultGenerator = NewGenerator()

func New() ULID {
	return defaultGenerator.New()
}
