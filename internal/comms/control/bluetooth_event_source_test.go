package control

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestBlueALSAReleasesPTTWhenRFCOMMCloses(t *testing.T) {
	for _, reason := range []string{"EOF", "interface removed", "normal release"} {
		t.Run(reason, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			monitorCtx, stopMonitor := context.WithCancel(ctx)
			reader, writer := io.Pipe()
			source := &blueALSAXEventSource{
				log: zerolog.Nop(),
				openRFCOMM: func(context.Context, *dbus.Conn, dbus.ObjectPath) (io.ReadCloser, error) {
					return reader, nil
				},
			}
			events := make(chan PTTEvent, 4)
			done := make(chan struct{})
			go func() {
				defer close(done)
				source.monitorRFCOMM(ctx, monitorCtx, nil, "/org/bluealsa/test", events)
			}()
			t.Cleanup(func() {
				stopMonitor()
				cancel()
				_ = writer.Close()
				<-done
			})
			expect := func(want PTTEvent) {
				t.Helper()
				select {
				case event := <-events:
					require.Equal(t, want, event)
				case <-ctx.Done():
					t.Fatalf("timed out waiting for PTT event %v", want)
				}
			}

			_, err := io.WriteString(writer, "+XEVENT: PTT_DOWN\r\n")
			require.NoError(t, err)
			expect(PTTDown)
			switch reason {
			case "interface removed":
				stopMonitor()
			case "normal release":
				_, err := io.WriteString(writer, "+XEVENT: PTT_UP\r\n")
				require.NoError(t, err)
				expect(PTTUp)
				require.NoError(t, writer.Close())
			default:
				require.NoError(t, writer.Close())
			}
			if reason != "normal release" {
				expect(PTTUp)
			}
			<-done
			require.Empty(t, events)
		})
	}
}

func TestBlueALSACommandReplies(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"subscriber number", "AT+CNUM\r", "\r\nERROR\r\n"},
		{"optional caller ID", "AT+CLIP=1\r", "\r\nERROR\r\n"},
		{"PTT notification", "AT+XEVENT=PTT_DOWN\r", "\r\nOK\r\n"},
		{"channel notification", "AT+XEVENT=NEXT_CH\r", "\r\nOK\r\n"},
		{"case insensitive", "at+xevent=PTT_UP\r", "\r\nOK\r\n"},
		{"built in commands", "AT+BRSF=100\rAT+VGS=10\rAT+CIND?\r", ""},
		{"responses and passive events", "\r\nOK\r\n+XEVENT: PTT_UP\r\n", ""},
		{"unrecognized command", "AT+UNKNOWN=1\r", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			require.NoError(t, replyBlueALSACommands(&out, tc.input))
			require.Equal(t, tc.want, out.String())
		})
	}
}

func TestBlueALSAConsumerRepliesWithoutChangingPTT(t *testing.T) {
	var replies bytes.Buffer
	stream := struct {
		io.Reader
		io.Writer
	}{bytes.NewBufferString("AT+CLIP=1\rAT+XEVENT=PTT_DOWN\rAT+XEVENT=NEXT_CH\rAT+XEVENT=PTT_UP\r"), &replies}
	source := &blueALSAXEventSource{log: zerolog.Nop()}
	events := make(chan PTTEvent, 4)
	require.False(t, source.consumeRFCOMM(context.Background(), events, stream))
	require.Equal(t, "\r\nERROR\r\n\r\nOK\r\n\r\nOK\r\n\r\nOK\r\n", replies.String())
	require.Len(t, events, 2)
	require.Equal(t, PTTDown, <-events)
	require.Equal(t, PTTUp, <-events)
}

func TestBlueALSAReplyWriteFailure(t *testing.T) {
	r, w := io.Pipe()
	require.NoError(t, r.Close())
	t.Cleanup(func() { _ = w.Close() })
	require.ErrorIs(t, replyBlueALSACommands(w, "AT+CLIP=1\r"), io.ErrClosedPipe)
}

func TestBlueALSAChannelCommandsAreNotPTT(t *testing.T) {
	s := NewBlueALSAXEventSource(zerolog.Nop()).(*blueALSAXEventSource)
	ptt := make(chan PTTEvent, 4)
	require.True(t, s.emitEventName(context.Background(), ptt, "NEXT_CH"))
	require.True(t, s.emitEventName(context.Background(), ptt, "PREV_CH"))
	require.Empty(t, ptt)
	require.Equal(t, 1, <-s.ChannelEvents())
	require.Equal(t, -1, <-s.ChannelEvents())
}

func TestBlueALSAChannelQueueDoesNotBlockPTT(t *testing.T) {
	s := NewBlueALSAXEventSource(zerolog.Nop()).(*blueALSAXEventSource)
	ptt := make(chan PTTEvent, 4)
	for range 20 {
		require.True(t, s.emitEventName(context.Background(), ptt, "NEXT_CH"))
		require.True(t, s.emitEventName(context.Background(), ptt, "PREV_CH"))
	}
	require.True(t, s.emitEventName(context.Background(), ptt, "PTT_DOWN"))
	require.Equal(t, PTTDown, <-ptt)
}

func TestAccessoryVolumeOnlyAcceptsIncomingCommands(t *testing.T) {
	for _, tc := range []struct {
		line string
		gain int
		ok   bool
	}{
		{"AT+VGS=7\r", 7, true}, {"AT+VGS=0", 0, true}, {"at+vgs=15", 15, true},
		{"+VGS:0", 0, false}, {"AT+VGM=9", 0, false}, {"AT+VGS=16", 16, false},
		{"AT+VGS=-1", -1, false}, {"AT+VGS=bad", 0, false},
	} {
		gain, ok := blueALSAAccessoryVolume(tc.line)
		require.Equal(t, tc.ok, ok, tc.line)
		require.Equal(t, tc.gain, gain)
	}
}

func TestBluetoothStartupGainIsNotAUserAdjustment(t *testing.T) {
	s := NewBlueALSAXEventSource(zerolog.Nop()).(*blueALSAXEventSource)
	ptt := make(chan PTTEvent, 4)
	input := bytes.NewBufferString("AT+VGS=15\rAT+XEVENT=BSHM:2.1.1:le\rAT+VGS=7\r+VGS:0\r")
	require.False(t, s.consumeRFCOMM(context.Background(), ptt, input))
	require.Len(t, s.speakerVolumes, 1)
	require.Equal(t, 7, <-s.SpeakerVolumes())
	require.Empty(t, ptt)
}
