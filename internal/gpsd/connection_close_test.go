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

func TestReadGPSD_nilConnReturnsImmediately(t *testing.T) {
	g := &GPSService{Log: zerolog.Nop(), done: make(chan struct{})}

	returned := make(chan struct{})

	go func() {
		defer close(returned)

		g.readGPSD()
	}()

	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("readGPSD did not return with a nil connection")
	}
}

// TestReadGPSD_shutdownClosesConn covers the g.done exit path: a line arrives
// after Close was requested, the loop returns, and the socket is released.
func TestReadGPSD_shutdownClosesConn(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan struct{})

	g := &GPSService{Log: zerolog.Nop(), done: done, conn: client}

	returned := make(chan struct{})

	go func() {
		defer close(returned)

		g.readGPSD()
	}()

	close(done)

	// net.Pipe writes are synchronous, so this returns once readGPSD has
	// consumed the line and observed the closed done channel.
	_, err := server.Write([]byte("{\"class\":\"TPV\"}\n"))
	require.NoError(t, err)

	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("readGPSD did not return after shutdown")
	}

	_, err = client.Read(make([]byte, 1))
	assert.ErrorIs(t, err, io.ErrClosedPipe)

	g.mu.RLock()
	defer g.mu.RUnlock()

	assert.Nil(t, g.conn)
}

// TestReadGPSD_readErrorClosesConn covers the scanner error path (a read
// failure other than a clean EOF) and confirms the descriptor is still
// released.
func TestReadGPSD_readErrorClosesConn(t *testing.T) {
	client, server := net.Pipe()

	t.Cleanup(func() { _ = server.Close() })

	g := &GPSService{Log: zerolog.Nop(), done: make(chan struct{}), conn: client}

	returned := make(chan struct{})

	go func() {
		defer close(returned)

		g.readGPSD()
	}()

	// Closing our own end while the scanner is blocked in Read makes the
	// pipe return io.ErrClosedPipe, which the scanner reports as an error.
	require.NoError(t, client.Close())

	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("readGPSD did not return after a read error")
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	assert.Nil(t, g.conn)
}

// TestReadGPSD_doesNotClobberReplacementConn guards the compare-and-clear:
// if a newer connection was installed while the old read loop was still
// winding down, the old loop must close only its own socket.
func TestReadGPSD_doesNotClobberReplacementConn(t *testing.T) {
	oldClient, oldServer := net.Pipe()
	newClient, newServer := net.Pipe()

	t.Cleanup(func() {
		_ = newClient.Close()
		_ = newServer.Close()
	})

	g := &GPSService{Log: zerolog.Nop(), done: make(chan struct{}), conn: oldClient}

	returned := make(chan struct{})

	go func() {
		defer close(returned)

		g.readGPSD()
	}()

	// Feed one line so the loop is definitely running on oldClient, then
	// swap in the replacement and drop the old peer.
	_, err := oldServer.Write([]byte("{\"class\":\"SKY\"}\n"))
	require.NoError(t, err)

	g.mu.Lock()
	g.conn = newClient
	g.mu.Unlock()

	require.NoError(t, oldServer.Close())

	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("readGPSD did not return after the old peer closed")
	}

	_, err = oldClient.Read(make([]byte, 1))
	assert.ErrorIs(t, err, io.ErrClosedPipe, "old socket must be closed")

	g.mu.RLock()
	defer g.mu.RUnlock()

	assert.Same(t, newClient, g.conn, "replacement connection must be left in place")
}
