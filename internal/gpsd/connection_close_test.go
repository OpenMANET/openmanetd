package gpsd

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadGPSD_closesConnOnLoss pins the regression where a lost GPSD
// connection was abandoned unclosed before reconnecting, leaking the
// descriptor until the runtime finalizer happened to run.
func TestReadGPSD_closesConnOnLoss(t *testing.T) {
	client, server := net.Pipe()

	g := &GPSService{
		Log:  zerolog.Nop(),
		done: make(chan struct{}),
		conn: client,
	}

	returned := make(chan struct{})

	go func() {
		defer close(returned)

		g.readGPSD()
	}()

	// Simulate GPSD going away.
	require.NoError(t, server.Close())

	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("readGPSD did not return after the peer closed")
	}

	// A locally closed pipe end reads io.ErrClosedPipe; one that was merely
	// abandoned after the peer hung up reads io.EOF instead.
	_, err := client.Read(make([]byte, 1))
	assert.ErrorIs(t, err, io.ErrClosedPipe, "lost connection should be closed by readGPSD")

	g.mu.RLock()
	defer g.mu.RUnlock()

	assert.Nil(t, g.conn, "g.conn should be cleared after the connection is lost")
}
