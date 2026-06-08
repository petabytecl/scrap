package encryption

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

const (
	defaultFakeKeyName = "scrap-documents"
	fakeWrappedPrefix  = "fake-transit:v"
	dataKeyBits128     = 128
	dataKeyBits256     = 256
	dataKeyBits512     = 512
)

type FakeConfig struct {
	KeyName        string
	Unavailable    bool
	AuthDenied     bool
	MissingKey     bool
	MinimumVersion int
}

type FakeTransit struct {
	mu             sync.Mutex
	keyName        string
	unavailable    bool
	authDenied     bool
	missingKey     bool
	currentVersion int
	minimumVersion int
	sequence       uint64
	keys           map[string]fakeDataKey
}

type fakeDataKey struct {
	plaintext []byte
	context   string
	version   int
}

func NewFakeTransit(cfg FakeConfig) *FakeTransit {
	keyName := strings.TrimSpace(cfg.KeyName)
	if keyName == "" {
		keyName = defaultFakeKeyName
	}
	minimumVersion := cfg.MinimumVersion
	if minimumVersion < 1 {
		minimumVersion = 1
	}
	return &FakeTransit{
		keyName:        keyName,
		unavailable:    cfg.Unavailable,
		authDenied:     cfg.AuthDenied,
		missingKey:     cfg.MissingKey,
		currentVersion: 1,
		minimumVersion: minimumVersion,
		keys:           map[string]fakeDataKey{},
	}
}

func (*FakeTransit) ProductionCapable() bool {
	return false
}

func (t *FakeTransit) GenerateDataKey(_ context.Context, req GenerateDataKeyRequest) (DataKey, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.stateError(); err != nil {
		return DataKey{}, err
	}
	keyBytes, err := requestedKeyBytes(req.Bits)
	if err != nil {
		return DataKey{}, err
	}
	t.sequence++
	plaintext := t.derivePlaintext(req.Context, keyBytes, t.sequence)
	wrappedKey := t.wrapLocked(t.currentVersion, plaintext, req.Context, t.sequence)
	return DataKey{
		Plaintext:  cloneBytes(plaintext),
		WrappedKey: wrappedKey,
		Version:    t.currentVersion,
	}, nil
}

func (t *FakeTransit) UnwrapDataKey(_ context.Context, req UnwrapDataKeyRequest) (UnwrappedDataKey, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key, err := t.lookupLocked(req.WrappedKey, req.Context)
	if err != nil {
		return UnwrappedDataKey{}, err
	}
	return UnwrappedDataKey{Plaintext: cloneBytes(key.plaintext), Version: key.version}, nil
}

func (t *FakeTransit) RewrapDataKey(_ context.Context, req RewrapDataKeyRequest) (RewrappedKey, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key, err := t.lookupLocked(req.WrappedKey, req.Context)
	if err != nil {
		return RewrappedKey{}, err
	}
	targetVersion := t.currentVersion
	if req.KeyVersion > 0 {
		targetVersion = req.KeyVersion
	}
	if targetVersion > t.currentVersion {
		return RewrappedKey{}, fmt.Errorf("transit fake future key version: %w", ErrInvalidRequest)
	}
	if key.version >= targetVersion {
		return RewrappedKey{WrappedKey: req.WrappedKey, Version: key.version}, nil
	}
	t.sequence++
	wrappedKey := t.wrapLocked(targetVersion, key.plaintext, req.Context, t.sequence)
	return RewrappedKey{WrappedKey: wrappedKey, Version: targetVersion, Changed: true}, nil
}

func (t *FakeTransit) Readiness(context.Context) (Readiness, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.stateError(); err != nil {
		return Readiness{}, err
	}
	return Readiness{
		Ready:                    true,
		LatestVersion:            t.currentVersion,
		MinimumDecryptionVersion: t.minimumVersion,
	}, nil
}

func (t *FakeTransit) Rotate() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.currentVersion++
}

func (t *FakeTransit) RequireMinimumVersion(version int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if version < 1 {
		version = 1
	}
	t.minimumVersion = version
}

func (t *FakeTransit) stateError() error {
	switch {
	case t.unavailable:
		return fmt.Errorf("transit fake unavailable: %w", ErrUnavailable)
	case t.authDenied:
		return fmt.Errorf("transit fake denied: %w", ErrAuthDenied)
	case t.missingKey:
		return fmt.Errorf("transit fake missing key: %w", ErrMissingKey)
	default:
		return nil
	}
}

func (t *FakeTransit) lookupLocked(wrappedKey string, context []byte) (fakeDataKey, error) {
	if err := t.stateError(); err != nil {
		return fakeDataKey{}, err
	}
	key, ok := t.keys[wrappedKey]
	if !ok {
		return fakeDataKey{}, fmt.Errorf("transit fake missing key: %w", ErrMissingKey)
	}
	if key.version < t.minimumVersion {
		return fakeDataKey{}, fmt.Errorf("transit fake minimum version: %w", ErrMinimumVersion)
	}
	if key.context != contextHandle(context) {
		return fakeDataKey{}, fmt.Errorf("transit fake context mismatch: %w", ErrAuthDenied)
	}
	return key, nil
}

func (t *FakeTransit) derivePlaintext(context []byte, size int, sequence uint64) []byte {
	plaintext := make([]byte, 0, size)
	for len(plaintext) < size {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s|%d|%d", t.keyName, t.currentVersion, contextHandle(context), sequence, len(plaintext))))
		plaintext = append(plaintext, sum[:]...)
	}
	return plaintext[:size]
}

func (t *FakeTransit) wrapLocked(version int, plaintext, context []byte, sequence uint64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s|%d", t.keyName, version, contextHandle(context), sequence)))
	wrappedKey := fmt.Sprintf("%s%d:%s", fakeWrappedPrefix, version, hex.EncodeToString(sum[:8]))
	t.keys[wrappedKey] = fakeDataKey{
		plaintext: cloneBytes(plaintext),
		context:   contextHandle(context),
		version:   version,
	}
	return wrappedKey
}

func requestedKeyBytes(bits int) (int, error) {
	bits = dataKeyBits(bits)
	switch bits {
	case dataKeyBits128, dataKeyBits256, dataKeyBits512:
		return bits / bitsPerByte, nil
	default:
		return 0, fmt.Errorf("transit data key bits must be 128, 256, or 512: %w", ErrInvalidRequest)
	}
}

func contextHandle(context []byte) string {
	if len(context) == 0 {
		return ""
	}
	sum := sha256.Sum256(context)
	return hex.EncodeToString(sum[:8])
}

func versionFromWrappedKey(wrappedKey string) int {
	if strings.HasPrefix(wrappedKey, fakeWrappedPrefix) {
		rest := strings.TrimPrefix(wrappedKey, fakeWrappedPrefix)
		rawVersion, _, _ := strings.Cut(rest, ":")
		version, _ := strconv.Atoi(rawVersion)
		return version
	}
	if strings.HasPrefix(wrappedKey, "vault:v") {
		rest := strings.TrimPrefix(wrappedKey, "vault:v")
		rawVersion, _, _ := strings.Cut(rest, ":")
		version, _ := strconv.Atoi(rawVersion)
		return version
	}
	return 0
}
