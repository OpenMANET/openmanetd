package meshjoin

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// RenderSVG encodes text as a QR code (error-correction level M, chosen
// for photos taken in direct sun) and returns it as an SVG document.
//
// The bitmap from go-qrcode already includes the 4-module quiet zone.
// The SVG carries only a viewBox so CSS decides the rendered size, and
// modules use fill="currentColor" so the host element's color applies.
// Horizontal runs of set modules are merged into one path segment each
// to keep the document small.
func RenderSVG(text string) (string, error) {
	if text == "" {
		return "", errors.New("meshjoin: empty text")
	}

	qr, err := qrcode.New(text, qrcode.Medium)
	if err != nil {
		return "", fmt.Errorf("encode qr: %w", err)
	}

	bitmap := qr.Bitmap()
	size := len(bitmap)

	var b strings.Builder
	// Rough upper bound: half the modules set, ~12 bytes per run.
	b.Grow(size*size*6 + 192)

	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 `)
	b.WriteString(strconv.Itoa(size))
	b.WriteByte(' ')
	b.WriteString(strconv.Itoa(size))
	b.WriteString(`" shape-rendering="crispEdges" role="img" aria-label="Mesh join QR code"><path fill="currentColor" d="`)

	for y, row := range bitmap {
		x := 0

		for x < len(row) {
			if !row[x] {
				x++

				continue
			}

			run := 0
			for x+run < len(row) && row[x+run] {
				run++
			}

			// M<x> <y>h<run>v1h-<run>z draws one horizontal run of modules.
			b.WriteByte('M')
			b.WriteString(strconv.Itoa(x))
			b.WriteByte(' ')
			b.WriteString(strconv.Itoa(y))
			b.WriteByte('h')
			b.WriteString(strconv.Itoa(run))
			b.WriteString("v1h-")
			b.WriteString(strconv.Itoa(run))
			b.WriteByte('z')

			x += run
		}
	}

	b.WriteString(`"/></svg>`)

	return b.String(), nil
}
