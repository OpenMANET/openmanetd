package camera

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/coreywagehoft/go-tak/pkg/cot"
	"github.com/openmanet/openmanetd/internal/network"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetect_missingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	present, err := Detect(context.Background())
	require.NoError(t, err)
	assert.False(t, present)
}

func TestDetect_camera(t *testing.T) {
	writeCamCommand(t, "#!/bin/sh\nprintf 'Available cameras:\\n1: imx219\\n'\n")

	present, err := Detect(context.Background())
	require.NoError(t, err)
	assert.True(t, present)
}

func TestDetect_commandError(t *testing.T) {
	writeCamCommand(t, "#!/bin/sh\nexit 1\n")

	present, err := Detect(context.Background())
	require.Error(t, err)
	assert.False(t, present)
	assert.ErrorContains(t, err, "list cameras")
}

func TestCameraListContainsSensor_output(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{name: "camera", output: "Available cameras:\n------------------\n0 : imx219 [3280x2464]", want: true},
		{name: "no camera", output: "Available cameras:\n------------------\n", want: false},
		{name: "unrelated colon", output: "Available cameras:\nnone: detected", want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, cameraListContainsSensor([]byte(tc.output)))
		})
	}
}

func TestResolveStream_configuredEndpoint(t *testing.T) {
	t.Parallel()

	reader := newFakeConfigReader(t, map[string][]string{
		"openmanet_video.playback.transport":      {"rtsp"},
		"camera-onvif-server.rpicamera.interface": {"ahwlan"},
		"camera-onvif-server.rpicamera.rtsp_name": {"camera/main"},
		"network.ahwlan.device":                   {"br-ahwlan"},
		"mediamtx.@mediamtx[0].rtsp_address":      {":8554"},
	})
	lookup := func(name string) network.NetworkInterface {
		assert.Equal(t, network.DefaultBridgeInterfaceName, name)

		return network.NetworkInterface{IP: []network.IPAddress{{IP: net.ParseIP("10.41.0.1")}}}
	}

	stream, err := resolveCameraStreamWith(context.Background(), reader, lookup)
	require.NoError(t, err)

	assert.Equal(t, Stream{Protocol: "rtsp", Address: "10.41.0.1", Port: 8554, Path: "/camera/main"}, stream)
	event := BuildMessage(testPosition(t), Node{UID: "camera-test"}, stream).GetCotEvent()
	detail, err := cot.DetailsFromString(event.GetDetail().GetXmlDetail())
	require.NoError(t, err)
	video := detail.GetFirst("__video")
	assert.Equal(t, "rtsp://10.41.0.1:8554/camera/main", video.GetAttr("url"))
	entry := video.GetFirst("ConnectionEntry")
	assert.Equal(t, "rtsp", entry.GetAttr("protocol"))
	assert.Equal(t, "8554", entry.GetAttr("port"))
	assert.Equal(t, "/camera/main", entry.GetAttr("path"))
}

func TestResolveStream_defaults(t *testing.T) {
	t.Parallel()

	stream, err := resolveCameraStreamWith(context.Background(), newFakeConfigReader(t, nil), func(name string) network.NetworkInterface {
		assert.Equal(t, network.DefaultBridgeInterfaceName, name)

		return network.NetworkInterface{IP: []network.IPAddress{
			{IP: net.ParseIP("127.0.0.1")},
			{IP: net.ParseIP("10.41.2.3")},
		}}
	})
	require.NoError(t, err)
	assert.Equal(t, Stream{Protocol: "srt", Address: "10.41.2.3", Port: defaultSRTPort, Path: "/" + defaultRTSPName}, stream)
}

func TestResolveStream_missingIPv4Address(t *testing.T) {
	t.Parallel()

	_, err := resolveCameraStreamWith(context.Background(), newFakeConfigReader(t, nil), func(string) network.NetworkInterface {
		return network.NetworkInterface{IP: []network.IPAddress{{IP: net.ParseIP("127.0.0.1")}}}
	})
	require.Error(t, err)
}

func TestResolveStream_canceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	lookupCalled := false
	_, err := resolveCameraStreamWith(ctx, newFakeConfigReader(t, nil), func(string) network.NetworkInterface {
		lookupCalled = true

		return network.NetworkInterface{}
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, lookupCalled)
}

func TestResolveStream_publicWrapperCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ResolveStream(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestResolveStream_contextCanceledDuringLookup(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	_, err := resolveCameraStreamWith(ctx, newFakeConfigReader(t, nil), func(string) network.NetworkInterface {
		cancel()

		return network.NetworkInterface{IP: []network.IPAddress{{IP: net.ParseIP("10.41.2.3")}}}
	})
	require.ErrorIs(t, err, context.Canceled)
}

func TestStream_URL(t *testing.T) {
	t.Parallel()

	stream := Stream{Protocol: "rtsp", Address: "10.41.0.1", Path: "/rpicamera main", Port: 8554}
	assert.Equal(t, "rtsp://10.41.0.1:8554/rpicamera%20main", stream.URL())
}

func writeCamCommand(t *testing.T, contents string) {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cam"), []byte(contents), 0o755))
	t.Setenv("PATH", dir)
}
