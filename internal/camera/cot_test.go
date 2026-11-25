package camera

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"testing"

	"github.com/coreywagehoft/go-tak/pkg/cot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildMessage_replacesRadioMarker(t *testing.T) {
	t.Parallel()

	event := BuildMessage(testPosition(t), Node{Callsign: "NODE-MANET", UID: "NODE-MANET"}, testStream(t)).GetCotEvent()
	assert.Equal(t, cameraSensorType, event.GetType())
	assert.Equal(t, "NODE-MANET", event.GetUid())
	assert.NotEqual(t, "a-f-G-U-U-S-R", event.GetType())
}

func TestBuildMessage_containsVideo(t *testing.T) {
	t.Parallel()

	event := BuildMessage(testPosition(t), Node{Callsign: "NODE-MANET", UID: "NODE-MANET"}, testStream(t)).GetCotEvent()

	detail, err := cot.DetailsFromString(event.GetDetail().GetXmlDetail())
	require.NoError(t, err)

	video := detail.GetFirst("__video")
	connection := video.GetFirst("ConnectionEntry")

	assert.Equal(t, "NODE-MANET-video", video.GetAttr("uid"))
	assert.Equal(t, testStream(t).URL(), video.GetAttr("url"))
	assert.Equal(t, video.GetAttr("uid"), connection.GetAttr("uid"))
	assert.Equal(t, testStream(t).Address, connection.GetAttr("address"))
	assert.Equal(t, "8554", connection.GetAttr("port"))
	assert.Equal(t, "true", detail.GetFirst("sensor").GetAttr("hideFov"))
}

func TestBuildMessage_meshPacketRoundTrip(t *testing.T) {
	t.Parallel()

	message := BuildMessage(testPosition(t), Node{Callsign: "NODE-MANET", UID: "NODE-MANET"}, testStream(t))

	data, err := cot.MakeProtoMeshPacketV1(message)
	require.NoError(t, err)

	decoded, version, err := cot.ReadProtoMesh(bufio.NewReader(bytes.NewReader(data)))
	require.NoError(t, err)
	assert.Equal(t, cot.ProtoVersion1, version)
	assert.Equal(t, message.GetCotEvent().GetUid(), decoded.GetCotEvent().GetUid())
}

func TestPublishCameraMessage_bridgeAndWrites(t *testing.T) {
	t.Parallel()

	writer := &fakeMulticastPacketWriter{}
	bridge := &net.Interface{Index: 7, Name: "br-ahwlan"}
	destination := &net.UDPAddr{IP: net.ParseIP("239.2.3.1"), Port: cameraMulticastPort}
	message := BuildMessage(testPosition(t), Node{Callsign: "NODE-MANET", UID: "NODE-MANET"}, testStream(t))

	require.NoError(t, publishCameraMessage(context.Background(), writer, bridge, destination, message))

	gotBridge, gotTTL, gotWrites := writer.state()
	assert.Same(t, bridge, gotBridge)
	assert.Equal(t, cameraMulticastTTL, gotTTL)
	assert.Equal(t, 2, gotWrites)
}

func TestPublishCameraMessage_socketErrors(t *testing.T) {
	t.Parallel()

	bridge := &net.Interface{Name: "br-ahwlan"}
	destination := &net.UDPAddr{IP: net.ParseIP("239.2.3.1"), Port: cameraMulticastPort}
	message := BuildMessage(testPosition(t), Node{Callsign: "NODE-MANET", UID: "NODE-MANET"}, testStream(t))

	tests := []struct {
		name    string
		writer  *fakeMulticastPacketWriter
		wantErr string
	}{
		{name: "TTL", writer: &fakeMulticastPacketWriter{TTLErr: errors.New("TTL failed")}, wantErr: "set ATAK multicast TTL"},
		{name: "interface", writer: &fakeMulticastPacketWriter{InterfaceErr: errors.New("interface failed")}, wantErr: "set ATAK multicast interface"},
		{name: "write", writer: &fakeMulticastPacketWriter{WriteErr: errors.New("write failed")}, wantErr: "write ATAK multicast packet"},
		{name: "second write", writer: &fakeMulticastPacketWriter{WriteErr: errors.New("second write failed"), WriteErrAt: 2}, wantErr: "write ATAK multicast packet"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := publishCameraMessage(context.Background(), tc.writer, bridge, destination, message)
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestPublishCameraMessage_contextCanceledBeforeWrite(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	writer := &fakeMulticastPacketWriter{Cancel: cancel}
	bridge := &net.Interface{Name: "br-ahwlan"}
	destination := &net.UDPAddr{IP: net.ParseIP("239.2.3.1"), Port: cameraMulticastPort}
	message := BuildMessage(testPosition(t), Node{Callsign: "NODE-MANET", UID: "NODE-MANET"}, testStream(t))

	err := publishCameraMessage(ctx, writer, bridge, destination, message)
	require.ErrorIs(t, err, context.Canceled)

	_, _, writes := writer.state()
	assert.Zero(t, writes)
}

func TestPublish_canceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Publish(ctx, testPosition(t), Node{Callsign: "NODE-MANET", UID: "NODE-MANET"}, testStream(t))
	require.ErrorIs(t, err, context.Canceled)
}

func TestPublishCameraMessage_canceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	writer := &fakeMulticastPacketWriter{}
	bridge := &net.Interface{Name: "br-ahwlan"}
	destination := &net.UDPAddr{IP: net.ParseIP("239.2.3.1"), Port: cameraMulticastPort}
	message := BuildMessage(testPosition(t), Node{Callsign: "NODE-MANET", UID: "NODE-MANET"}, testStream(t))

	err := publishCameraMessage(ctx, writer, bridge, destination, message)
	require.ErrorIs(t, err, context.Canceled)

	_, _, writes := writer.state()
	assert.Zero(t, writes)
}

func testPosition(t *testing.T) Position {
	t.Helper()

	return Position{
		Altitude:        50,
		CE:              2,
		GeoidSeparation: 15,
		Lat:             37.7749,
		LE:              3,
		Lon:             -122.4194,
		Speed:           5,
		Track:           90,
	}
}

func testStream(t *testing.T) Stream {
	t.Helper()

	return Stream{Protocol: "rtsp", Address: "10.41.0.1", Path: "/" + defaultRTSPName, Port: 8554}
}

func TestBuildMessage_srtPlayback(t *testing.T) {
	t.Parallel()
	stream := Stream{Protocol: "srt", Address: "10.41.2.3", Port: 8890, Path: "/camera/main", LatencyMS: 200}
	event := BuildMessage(testPosition(t), Node{UID: "node"}, stream).GetCotEvent()
	detail, err := cot.DetailsFromString(event.GetDetail().GetXmlDetail())
	require.NoError(t, err)
	video := detail.GetFirst("__video")
	assert.Equal(t, "srt://10.41.2.3:8890?streamid=read:camera/main&latency=200000", video.GetAttr("url"))
	entry := video.GetFirst("ConnectionEntry")
	assert.Equal(t, "srt", entry.GetAttr("protocol"))
	assert.Equal(t, "/?streamid=read:camera/main&latency=200000", entry.GetAttr("path"))
}
