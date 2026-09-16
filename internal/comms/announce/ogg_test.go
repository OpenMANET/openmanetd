package announce

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeOggPage builds a minimal Ogg page: "OggS", 22 zero bytes
// (version, header type, granule, serial, sequence, CRC — the parser
// checks none of them), segment count, lacing table, body.
func makeOggPage(lacing []byte, body []byte) []byte {
	page := make([]byte, 0, 27+len(lacing)+len(body))
	page = append(page, "OggS"...)
	page = append(page, make([]byte, 22)...)
	page = append(page, byte(len(lacing)))
	page = append(page, lacing...)
	page = append(page, body...)

	return page
}

func TestOggPackets_SkipsEmptyPackets(t *testing.T) {
	// Five packets laced [1, empty, 1, 1, 1]. The zero-length packet is
	// invalid for an Opus stream and must be skipped so the decoder never
	// sees it; the two leading real packets are dropped as headers.
	lacing := []byte{1, 0, 1, 1, 1}
	body := []byte{0xA, 0xB, 0xC, 0xD}

	pkts, err := oggPackets(makeOggPage(lacing, body))
	require.NoError(t, err)
	require.Len(t, pkts, 2)

	for i, p := range pkts {
		assert.NotEmpty(t, p, "packet %d must not be empty", i)
	}

	assert.Equal(t, []byte{0xC}, pkts[0])
	assert.Equal(t, []byte{0xD}, pkts[1])
}

func TestOggPackets_AllEmptyPacketsIsError(t *testing.T) {
	// Only empty packets: nothing survives the skip, so the stream is
	// rejected as too short rather than returning header-less garbage.
	_, err := oggPackets(makeOggPage([]byte{0, 0, 0}, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too few ogg packets")
}
