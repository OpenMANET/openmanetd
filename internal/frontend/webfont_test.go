package frontend

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStaticFileServer_servesWoff2WithFontType pins that the self-hosted
// webfonts come back typed as fonts rather than as a generic blob.
//
// Nothing in this package registers the mapping: Go's built-in extension
// table has no .woff2 entry, but its content sniffer recognizes the WOFF2
// signature, so http.ServeContent arrives at the right type on its own. That
// is worth a test precisely because it is implicit — the fonts moved
// in-tree in the same change that removed the Google Fonts <link>, and
// nothing else would catch them regressing to application/octet-stream.
func TestStaticFileServer_servesWoff2WithFontType(t *testing.T) {
	// The four-byte signature at the head of every WOFF2 file, plus the
	// version and length bytes that follow it.
	woff2Magic := []byte("wOF2\x00\x01\x00\x00")

	staticFS := fstest.MapFS{
		"assets/jetbrains-mono-abc123.woff2": &fstest.MapFile{Data: woff2Magic},
	}
	srv := httptest.NewServer(http.FileServerFS(staticFS))
	t.Cleanup(srv.Close)

	res, err := srv.Client().Get(srv.URL + "/assets/jetbrains-mono-abc123.woff2")
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Contains(t, res.Header.Get("Content-Type"), "font/woff2")
}
