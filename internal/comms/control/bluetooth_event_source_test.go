package control

import (
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
