package meshjoin_test

import (
	"strings"
	"testing"

	meshjoinv1 "github.com/openmanet/openmanetd/internal/api/openmanet/mesh_join/v1"
	wificonfigv1 "github.com/openmanet/openmanetd/internal/api/openmanet/wifi_config/v1"
	"github.com/openmanet/openmanetd/internal/meshjoin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func samplePayload() *meshjoinv1.MeshJoinPayload {
	return &meshjoinv1.MeshJoinPayload{
		SourceHostname: "alpha",
		Halow: &meshjoinv1.MeshCredentials{
			MeshId:       "field-mesh",
			Passphrase:   "correct-horse",
			Encryption:   wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE,
			BandwidthMhz: 8,
			Channel:      44,
			CountryCode:  "US",
		},
		Backhaul: &meshjoinv1.MeshCredentials{
			MeshId:       "field-mesh-2g",
			Passphrase:   "backhaul-pass",
			Encryption:   wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE,
			BandwidthMhz: 20,
			Channel:      8,
			CountryCode:  "US",
		},
	}
}

// worstCasePayload fills every string to its proto maximum with both
// meshes present, the largest thing GetMeshJoinQR can ever emit.
func worstCasePayload() *meshjoinv1.MeshJoinPayload {
	creds := func() *meshjoinv1.MeshCredentials {
		return &meshjoinv1.MeshCredentials{
			MeshId:       strings.Repeat("m", 32),
			Passphrase:   strings.Repeat("p", 63),
			Encryption:   wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE,
			BandwidthMhz: 160,
			Channel:      165,
			CountryCode:  "ABC",
		}
	}

	return &meshjoinv1.MeshJoinPayload{
		SourceHostname: strings.Repeat("h", 63),
		Halow:          creds(),
		Backhaul:       creds(),
	}
}

func TestEncode_PrefixAndRoundTrip(t *testing.T) {
	text, err := meshjoin.Encode(samplePayload())
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(text, meshjoin.Prefix), "text must start with the version marker")
	assert.NotContains(t, text, "=", "base64url without padding")
	assert.NotContains(t, text, "+")
	assert.NotContains(t, text, "/")

	got, err := meshjoin.Decode(text)
	require.NoError(t, err)
	assert.True(t, proto.Equal(samplePayload(), got), "decode must reproduce the payload")
}

func TestEncode_NilPayload(t *testing.T) {
	_, err := meshjoin.Encode(nil)
	assert.Error(t, err)
}

func TestDecode_Errors(t *testing.T) {
	tests := []struct {
		name string
		text string
		want error
	}{
		{name: "wifi string", text: "WIFI:S:foo;P:bar;;", want: meshjoin.ErrNotMeshJoin},
		{name: "empty", text: "", want: meshjoin.ErrNotMeshJoin},
		{name: "newer version", text: "OPENMANET2:AAAA", want: meshjoin.ErrUnsupportedVersion},
		{name: "family without version", text: "OPENMANETX:AAAA", want: meshjoin.ErrNotMeshJoin},
		{name: "bad base64", text: "OPENMANET1:%%%not-base64%%%", want: meshjoin.ErrCorrupt},
		{name: "bad protobuf", text: "OPENMANET1:_____w", want: meshjoin.ErrCorrupt},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := meshjoin.Decode(tc.text)
			assert.ErrorIs(t, err, tc.want)
		})
	}
}

func TestDecode_TrimsWhitespace(t *testing.T) {
	text, err := meshjoin.Encode(samplePayload())
	require.NoError(t, err)

	got, err := meshjoin.Decode("  " + text + "\n")
	require.NoError(t, err)
	assert.Equal(t, "field-mesh", got.GetHalow().GetMeshId())
}

func TestEncode_WorstCaseFitsQRVersion15M(t *testing.T) {
	text, err := meshjoin.Encode(worstCasePayload())
	require.NoError(t, err)

	// 415 bytes is the byte-mode capacity of a version 15 QR code at
	// error-correction level M.
	assert.LessOrEqual(t, len(text), 415, "worst-case text must fit QR v15-M")
}
