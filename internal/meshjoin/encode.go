// Package meshjoin encodes and decodes the "share mesh" QR payload: a
// MeshJoinPayload serialized as binary protobuf, base64url-encoded
// without padding and prefixed with the format version marker
// "OPENMANET1:". It also renders the text as an SVG QR code.
//
// The package has no UCI or handler dependencies so the CLI and tests
// can use it directly.
package meshjoin

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	meshjoinv1 "github.com/openmanet/openmanetd/internal/api/openmanet/mesh_join/v1"
	"google.golang.org/protobuf/proto"
)

const (
	// Prefix is the version marker every payload text starts with.
	Prefix = "OPENMANET1:"

	// prefixFamily is shared by every version of the marker.
	prefixFamily = "OPENMANET"
)

var (
	// ErrNotMeshJoin is returned by Decode when the text does not start
	// with an OPENMANET<n>: marker.
	ErrNotMeshJoin = errors.New("meshjoin: not an OpenMANET mesh code")

	// ErrUnsupportedVersion is returned when the marker names a version
	// this build does not understand.
	ErrUnsupportedVersion = errors.New("meshjoin: unsupported payload version")

	// ErrCorrupt is returned when the base64 or protobuf body cannot be
	// parsed.
	ErrCorrupt = errors.New("meshjoin: corrupt payload")
)

// Encode serializes p and returns the QR text.
func Encode(p *meshjoinv1.MeshJoinPayload) (string, error) {
	if p == nil {
		return "", errors.New("meshjoin: nil payload")
	}

	raw, err := proto.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	return Prefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// Decode parses QR text produced by Encode. Surrounding whitespace is
// ignored so pasted text survives a trailing newline.
func Decode(text string) (*meshjoinv1.MeshJoinPayload, error) {
	text = strings.TrimSpace(text)

	body, ok := strings.CutPrefix(text, Prefix)
	if !ok {
		if isVersionMarker(text) {
			return nil, ErrUnsupportedVersion
		}

		return nil, ErrNotMeshJoin
	}

	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	p := &meshjoinv1.MeshJoinPayload{}
	if err := proto.Unmarshal(raw, p); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	return p, nil
}

// isVersionMarker reports whether text starts with OPENMANET<digits>:
// for some version other than the one Decode understands.
func isVersionMarker(text string) bool {
	rest, ok := strings.CutPrefix(text, prefixFamily)
	if !ok {
		return false
	}

	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}

	return digits > 0 && digits < len(rest) && rest[digits] == ':'
}
