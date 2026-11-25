package frontend

import (
	"github.com/digineo/go-uci/v2"
	"github.com/openmanet/openmanetd/internal/camera"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSRTCallerURL(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"srt://node:8890?streamid=read:cam&latency=120000", "srt://node:8890?latency=120&streamid=read%3Acam"},
		{"srt://node:8890?mode=listener", ""},
		{"srt://node:0", ""},
		{"http://node:8890", ""},
	} {
		got, err := srtCallerURL(tc.url)
		if got != tc.want || (err != nil) != (tc.want == "") {
			t.Errorf("srtCallerURL(%q) = %q, %v", tc.url, got, err)
		}
	}
}

func TestSaveVideoRejectsUnsupportedResolution(t *testing.T) {
	r := httptest.NewRequest("PUT", "/api/settings/video", strings.NewReader(`{"transport":"srt","hardwareAcceleration":true,"resolution":"4k"}`))
	w := httptest.NewRecorder()
	new(Server).saveVideoSettings(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "Unsupported camera resolution") {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
	}
}

func TestSaveVideoSettingsPushWithCameraCoTDisabled(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, videoConfig), []byte("config video 'playback'\n"), 0600))
	server := new(Server)
	for _, added := range []string{`,"cameraCoTEnabled":false`, ""} {
		// The second save models an older client that omits the CoT preference.
		body := `{"transport":"rtsp","hardwareAcceleration":true,"push":true,"destination":"srt://receiver:8890?streamid=publish:camera"` + added + "}"
		r := httptest.NewRequest("PUT", "/api/settings/video", strings.NewReader(body))
		w := httptest.NewRecorder()
		server.saveVideoSettingsWith(w, r, dir, "/bin/true")
		require.Equal(t, 200, w.Code, w.Body.String())
		tree := uci.NewTree(dir)
		require.NoError(t, tree.LoadConfig(videoConfig, false))
		require.False(t, camera.CoTEnabled(tree))
		require.Equal(t, "rtsp", option(tree, videoConfig, "playback", "transport", ""))
		require.Equal(t, "1", option(tree, videoConfig, "playback", "push_enabled", ""))
		require.Equal(t, "srt://receiver:8890?streamid=publish:camera", option(tree, videoConfig, "playback", "push_destination", ""))
	}
}

func TestAdaptiveSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, videoConfig), []byte("config video 'playback'\n"), 0600))
	for _, tc := range []struct {
		field  string
		status int
	}{
		{"," + `"adaptive":{"enabled":true,"minKbps":125,"maxKbps":1000}`, 200},
		{"," + `"adaptive":{"enabled":false,"minKbps":999,"maxKbps":500}`, 400},
		{"", 200},
	} {
		body := `{"transport":"srt","hardwareAcceleration":true` + tc.field + "}"
		req := httptest.NewRequest("PUT", "/api/settings/video", strings.NewReader(body))
		res := httptest.NewRecorder()
		new(Server).saveVideoSettingsWith(res, req, dir, "/bin/true")
		require.Equal(t, tc.status, res.Code, res.Body.String())
		require.Equal(t, &adaptiveOptions{true, 125, 1000}, adaptiveSettings(uci.NewTree(dir)))
	}
}
