package comms

import (
	"net"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/openmanet/openmanetd/internal/comms/rtp"
)

// newQoSTestConn opens a connected loopback UDP socket. Dialing does not
// send any packets; the socket exists only to carry socket options.
func newQoSTestConn(t *testing.T) *net.UDPConn {
	t.Helper()

	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// sockoptInt reads one integer socket option straight from the kernel so
// assertions are independent of setQoSMarking's own read-back path.
func sockoptInt(t *testing.T, conn *net.UDPConn, level, opt int) int {
	t.Helper()

	raw, err := conn.SyscallConn()
	require.NoError(t, err)

	var (
		val     int
		sockErr error
	)

	require.NoError(t, raw.Control(func(fd uintptr) {
		val, sockErr = unix.GetsockoptInt(int(fd), level, opt)
	}))
	require.NoError(t, sockErr)

	return val
}

// canSetSOPriority probes whether this process may set SO_PRIORITY values
// above 6 (needs CAP_NET_ADMIN). Unprivileged CI runners fail the set with
// EPERM; the priority half of the marking tests is skipped there.
func canSetSOPriority(t *testing.T) bool {
	t.Helper()

	conn := newQoSTestConn(t)

	raw, err := conn.SyscallConn()
	require.NoError(t, err)

	var sockErr error

	require.NoError(t, raw.Control(func(fd uintptr) {
		sockErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_PRIORITY, 261)
	}))

	return sockErr == nil
}

func TestSetQoSMarking_AppliesTOSAndPriority(t *testing.T) {
	t.Parallel()

	privileged := canSetSOPriority(t)

	tests := []struct {
		name     string
		dscp     int
		wantTOS  int
		wantPrio int
	}{
		{name: "EF maps to AC_VI", dscp: 46, wantTOS: 0xB8, wantPrio: 261},
		{name: "CS6 maps to AC_VO", dscp: 48, wantTOS: 0xC0, wantPrio: 262},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := newQoSTestConn(t)

			tos, prio := setQoSMarking(conn, tt.dscp, zerolog.Nop())

			// The TOS half needs no privilege: assert unconditionally,
			// against the kernel directly and against the read-back.
			assert.Equal(t, tt.wantTOS, sockoptInt(t, conn, unix.IPPROTO_IP, unix.IP_TOS))
			assert.Equal(t, tt.wantTOS, tos)

			if privileged {
				assert.Equal(t, tt.wantPrio, sockoptInt(t, conn, unix.SOL_SOCKET, unix.SO_PRIORITY))
				assert.Equal(t, tt.wantPrio, prio)
			} else {
				// Without CAP_NET_ADMIN the SO_PRIORITY set fails, but the
				// kernel still rewrites sk_priority as a side effect of
				// IP_TOS (rt_tos2priority legacy mapping), so the exact
				// value is kernel policy. Assert only that the read-back is
				// truthful about whatever the kernel holds.
				t.Log("skipping SO_PRIORITY value assertion: no CAP_NET_ADMIN in this environment")
				assert.Equal(t, sockoptInt(t, conn, unix.SOL_SOCKET, unix.SO_PRIORITY), prio)
			}
		})
	}
}

func TestSetQoSMarking_ZeroIsNoop(t *testing.T) {
	t.Parallel()

	conn := newQoSTestConn(t)

	tos, prio := setQoSMarking(conn, 0, zerolog.Nop())

	assert.Equal(t, 0, tos)
	assert.Equal(t, 0, prio)
	assert.Equal(t, 0, sockoptInt(t, conn, unix.IPPROTO_IP, unix.IP_TOS))
	assert.Equal(t, 0, sockoptInt(t, conn, unix.SOL_SOCKET, unix.SO_PRIORITY))
}

// extractSenderConn temporarily swaps the sender's underlying writer out to
// recover the real *net.UDPConn, then swaps it back so closePartial still
// closes the socket.
func extractSenderConn(t *testing.T, s *rtp.SwappableSender) *net.UDPConn {
	t.Helper()

	old := s.Swap(&mockWriter{})
	conn, ok := old.(*net.UDPConn)
	require.True(t, ok, "sender should wrap a *net.UDPConn")
	s.Swap(conn)

	return conn
}

func TestBuildSinglePortChannel_AppliesQoSMarking(t *testing.T) {
	t.Parallel()

	cfg := &CommsConfig{Log: zerolog.Nop(), DSCP: 46}
	mpc := McastPortConfig{Address: "239.192.41.1", Port: 38899, Send: true}

	pc, err := cfg.buildSinglePortChannel(mpc, "127.0.0.1", nil, 0x1234)
	require.NoError(t, err)
	t.Cleanup(pc.closePartial)

	rtpConn := extractSenderConn(t, pc.Sender)
	rtcpConn := extractSenderConn(t, pc.RTCPSend)

	// Both halves of the RTP/RTCP pair must ride the same class.
	assert.Equal(t, 0xB8, sockoptInt(t, rtpConn, unix.IPPROTO_IP, unix.IP_TOS))
	assert.Equal(t, 0xB8, sockoptInt(t, rtcpConn, unix.IPPROTO_IP, unix.IP_TOS))

	var snap PortSnapshot

	pc.Snapshot(&snap)
	assert.Equal(t, 46, snap.QoSDSCP)
	assert.Equal(t, sockoptInt(t, rtpConn, unix.SOL_SOCKET, unix.SO_PRIORITY), snap.QoSSOPriority)
}

func TestBuildSinglePortChannel_ZeroDSCPLeavesSocketsUnmarked(t *testing.T) {
	t.Parallel()

	cfg := &CommsConfig{Log: zerolog.Nop()}
	mpc := McastPortConfig{Address: "239.192.41.1", Port: 38897, Send: true}

	pc, err := cfg.buildSinglePortChannel(mpc, "127.0.0.1", nil, 0x1234)
	require.NoError(t, err)
	t.Cleanup(pc.closePartial)

	rtpConn := extractSenderConn(t, pc.Sender)
	rtcpConn := extractSenderConn(t, pc.RTCPSend)

	assert.Equal(t, 0, sockoptInt(t, rtpConn, unix.IPPROTO_IP, unix.IP_TOS))
	assert.Equal(t, 0, sockoptInt(t, rtcpConn, unix.IPPROTO_IP, unix.IP_TOS))

	var snap PortSnapshot

	pc.Snapshot(&snap)
	assert.Equal(t, 0, snap.QoSDSCP)
	assert.Equal(t, 0, snap.QoSSOPriority)
}
