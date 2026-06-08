package security

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/url"
	"strings"
)

const (
	peerIdentityHost         = "scrap"
	peerIdentityMaxPartBytes = 128
)

var errMissingCertificate = errors.New("missing certificate")

type peerIdentityContextKey struct{}

func ExtractPeerIdentity(state tls.ConnectionState) (PeerIdentityConfig, error) {
	cert, err := verifiedPeerCertificate(state)
	if err != nil {
		return PeerIdentityConfig{}, newGateError(ClassPeerIdentityPolicy, "peer certificate", "verified peer certificate is required")
	}
	for _, uri := range cert.URIs {
		identity, ok := parsePeerIdentityURI(uri)
		if ok {
			return identity, nil
		}
	}
	return PeerIdentityConfig{}, newGateError(ClassPeerIdentityPolicy, "peer certificate", "valid peer identity URI is required")
}

func ContextWithPeerIdentity(ctx context.Context, identity PeerIdentityConfig) context.Context {
	return context.WithValue(ctx, peerIdentityContextKey{}, identity)
}

func PeerIdentityFromContext(ctx context.Context) (PeerIdentityConfig, bool) {
	identity, ok := ctx.Value(peerIdentityContextKey{}).(PeerIdentityConfig)
	return identity, ok
}

func verifiedPeerCertificate(state tls.ConnectionState) (*x509.Certificate, error) {
	if len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
		return nil, errMissingCertificate
	}
	return state.VerifiedChains[0][0], nil
}

func parsePeerIdentityURI(uri *url.URL) (PeerIdentityConfig, bool) {
	if !isPeerIdentityURIBase(uri) {
		return PeerIdentityConfig{}, false
	}
	parts := strings.Split(strings.TrimPrefix(uri.EscapedPath(), "/"), "/")
	if len(parts) != 5 || parts[0] != "cell" || parts[2] != "member" {
		return PeerIdentityConfig{}, false
	}
	return peerIdentityFromParts(parts[1], parts[3], parts[4])
}

func isPeerIdentityURIBase(uri *url.URL) bool {
	return uri != nil &&
		uri.Scheme == "spiffe" &&
		uri.Host == peerIdentityHost &&
		uri.RawQuery == "" &&
		uri.Fragment == ""
}

func peerIdentityFromParts(cellPart, hostnamePart, memberPart string) (PeerIdentityConfig, bool) {
	cellID, ok := cleanIdentityPart(cellPart)
	if !ok {
		return PeerIdentityConfig{}, false
	}
	memberHostname, ok := cleanIdentityPart(hostnamePart)
	if !ok {
		return PeerIdentityConfig{}, false
	}
	memberID, ok := cleanIdentityPart(memberPart)
	if !ok {
		return PeerIdentityConfig{}, false
	}
	return PeerIdentityConfig{
		CellID:         cellID,
		MemberHostname: memberHostname,
		MemberID:       memberID,
	}, true
}

func cleanIdentityPart(part string) (string, bool) {
	value, err := url.PathUnescape(part)
	if err != nil {
		return "", false
	}
	value = strings.TrimSpace(value)
	if value == "" || len(value) > peerIdentityMaxPartBytes {
		return "", false
	}
	for _, r := range value {
		if !validIdentityRune(r) {
			return "", false
		}
	}
	return value, true
}

func validIdentityRune(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '-' ||
		r == '_' ||
		r == '.'
}
