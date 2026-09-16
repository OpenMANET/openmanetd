package meshjoin_test

import (
	"strings"
	"testing"

	"github.com/openmanet/openmanetd/internal/meshjoin"
	qrcode "github.com/skip2/go-qrcode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderSVG_Shape(t *testing.T) {
	svg, err := meshjoin.RenderSVG("OPENMANET1:AAAA")
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(svg, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 `), "svg must open with a viewBox")
	assert.True(t, strings.HasSuffix(svg, `"/></svg>`))
	assert.Contains(t, svg, `fill="currentColor"`)
	assert.Contains(t, svg, `shape-rendering="crispEdges"`)
	assert.NotContains(t, svg, ` width=`, "CSS decides the rendered size")
	assert.NotContains(t, svg, ` height=`)
}

func TestRenderSVG_Deterministic(t *testing.T) {
	a, err := meshjoin.RenderSVG("OPENMANET1:AAAA")
	require.NoError(t, err)

	b, err := meshjoin.RenderSVG("OPENMANET1:AAAA")
	require.NoError(t, err)

	assert.Equal(t, a, b)
}

func TestRenderSVG_ViewBoxMatchesBitmapWithQuietZone(t *testing.T) {
	const text = "OPENMANET1:AAAA"

	qr, err := qrcode.New(text, qrcode.Medium)
	require.NoError(t, err)

	size := len(qr.Bitmap())

	svg, err := meshjoin.RenderSVG(text)
	require.NoError(t, err)

	assert.Contains(t, svg, `viewBox="0 0 `+itoa(size)+" "+itoa(size)+`"`,
		"viewBox must span the bitmap, which already carries the 4-module quiet zone")
}

func TestRenderSVG_WorstCasePayloadIsVersion15OrSmaller(t *testing.T) {
	text, err := meshjoin.Encode(worstCasePayload())
	require.NoError(t, err)

	qr, err := qrcode.New(text, qrcode.Medium)
	require.NoError(t, err)

	assert.LessOrEqual(t, qr.VersionNumber, 15)
}

func TestRenderSVG_EmptyTextFails(t *testing.T) {
	_, err := meshjoin.RenderSVG("")
	assert.Error(t, err)
}

func itoa(n int) string {
	return strings.TrimSpace(strings.Repeat(" ", 0) + fmtInt(n))
}

func fmtInt(n int) string {
	if n == 0 {
		return "0"
	}

	var digits []byte

	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}

	return string(digits)
}
