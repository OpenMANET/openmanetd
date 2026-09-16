package comms

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/rs/zerolog"
	"golang.org/x/net/ipv4"
	"golang.org/x/sys/unix"

	"github.com/openmanet/openmanetd/internal/comms/device"
	"github.com/openmanet/openmanetd/internal/comms/rtp"
)

// rtpMulticastTTL is the IP TTL set on outgoing RTP/RTCP multicast packets.
// Voice must reach distant sites through the full batman-adv + VXLAN +
// Tailscale path, where each batman-adv hop and each bridge hop decrements
// TTL. 32 is generous enough for any realistic mesh diameter while still
// bounding stray packets that escape a misrouted tunnel. The prior value of
// 6 silently black-holed voice on multi-hop deployments.
const rtpMulticastTTL = 32

// rxSocketBufBytes is the requested SO_RCVBUF size for the RTP receive
// socket. 1 MiB absorbs bursty mesh arrivals when receiveLoop is briefly
// scheduled out (GC, scheduler hand-off, neighbor goroutine). At ~100-byte
// Opus payloads this is roughly 10000 frames of headroom — far more than
// any realistic stall. The kernel may clamp the actual value at
// net.core.rmem_max (typically 208 KB on stock Linux, but embedded targets
// usually raise this in sysctl); the post-set verification log records
// what we actually got so undersized rmem_max is observable.
const rxSocketBufBytes = 1 << 20

// listenRTPReceiver opens a UDP socket bound to addr with SO_REUSEPORT enabled.
//
// SO_REUSEPORT lets a second socket bind to the same port while the current
// receiver is still open. This is required for UpdateMulticastEndpoint when
// the port does not change: buildSinglePortChannel must be able to acquire
// the new socket before the old one is closed.
func listenRTPReceiver(addr *net.UDPAddr) (*net.UDPConn, error) {
	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				_ = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
			})
		},
	}

	pc, err := lc.ListenPacket(context.Background(), "udp4", addr.String())
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	conn, ok := pc.(*net.UDPConn)
	if !ok {
		_ = pc.Close()

		return nil, errors.New("listenRTPReceiver: unexpected PacketConn type")
	}

	return conn, nil
}

// setMulticastTTL sets the IP multicast TTL on a UDP socket.
func setMulticastTTL(conn *net.UDPConn, ttl int) error {
	if err := ipv4.NewPacketConn(conn).SetMulticastTTL(ttl); err != nil {
		return fmt.Errorf("set multicast TTL: %w", err)
	}

	return nil
}

// setQoSMarking applies voice QoS marking to an egress UDP socket and
// returns the kernel's resulting TOS byte and SO_PRIORITY value (both 0
// when dscp is 0 or the socket cannot be inspected).
//
// IP_TOS carries the DSCP on the wire — the only part relay hops can see:
// batman-adv re-derives skb->priority from the inner IP precedence
// (256 + dscp>>3) when forwarding a frame. SO_PRIORITY is set to that same
// derived value in the 256–263 802.1d passthrough range so the first hop
// classifies without payload inspection and any direct-wlan egress is
// covered. Deriving both from one DSCP keeps every hop in the same WMM
// access class.
//
// Marking failures are logged, never fatal: unmarked voice still flows.
// SO_PRIORITY above 6 needs CAP_NET_ADMIN (held under procd, where the
// daemon runs as root); on EPERM the socket continues TOS-only, which
// batman-adv still classifies from at every hop.
func setQoSMarking(conn *net.UDPConn, dscp int, log zerolog.Logger) (tos, prio int) {
	if dscp == 0 {
		return 0, 0
	}

	raw, err := conn.SyscallConn()
	if err != nil {
		log.Warn().Err(err).Msg("comms: QoS marking: syscall conn")

		return 0, 0
	}

	if controlErr := raw.Control(func(fd uintptr) {
		// Order matters: the kernel rewrites sk_priority to a legacy
		// TC_PRIO_* value as a side effect of IP_TOS (rt_tos2priority), so
		// SO_PRIORITY must be applied after IP_TOS or it would be clobbered.
		if tosErr := unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_TOS, dscp<<2); tosErr != nil {
			log.Warn().Err(tosErr).Int("dscp", dscp).Msg("comms: QoS marking: set IP_TOS")
		}

		if prioErr := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_PRIORITY, 256+(dscp>>3)); prioErr != nil {
			log.Warn().Err(prioErr).Int("dscp", dscp).
				Msg("comms: QoS marking: set SO_PRIORITY, continuing TOS-only")
		}

		// Read back what the kernel actually holds (mirroring the rx-buffer
		// requested-vs-actual pattern) so the debug log and the port
		// snapshot report applied state, not requested state. Read-back
		// errors leave the zero value in place — same signal as "not set".
		tos, _ = unix.GetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_TOS)
		prio, _ = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_PRIORITY)
	}); controlErr != nil {
		log.Warn().Err(controlErr).Msg("comms: QoS marking: control")

		return 0, 0
	}

	log.Debug().
		Int("dscp", dscp).
		Int("tos", tos).
		Int("so_priority", prio).
		Msg("comms: QoS marking applied")

	return tos, prio
}

// readUDPDrops resolves the kernel-drop scan through the test seam,
// falling back to the real /proc/net/udp reader when unset.
func (cfg *CommsConfig) readUDPDrops(localPort int) (int64, error) {
	if cfg.readUDPDropsFn != nil {
		return cfg.readUDPDropsFn(localPort)
	}

	return readUDPSocketDrops(localPort)
}

// readUDPSocketDrops returns the kernel's per-socket drop counter for the
// UDP socket bound to localPort, parsed out of /proc/net/udp and
// /proc/net/udp6. Returns -1 with no error if no matching row is found
// (e.g. on a non-Linux test host or before the socket is fully bound).
//
// The drops column is the canonical kernel counter for "packets dropped
// because the per-socket receive queue was full" — the only definitive
// signal that SO_RCVBUF or scheduling jitter is starving the receiver.
func readUDPSocketDrops(localPort int) (int64, error) {
	// /proc/net/udp{,6} format (one row per socket):
	//   sl  local_address rem_address st tx_q rx_q tr tm->when retrnsmt
	//     uid  timeout inode ref pointer drops
	// local_address is "IP:PORT" in big-endian hex; we only need the PORT
	// half so we don't have to byte-swap an IP. Drops is the last field.
	for _, path := range [...]string{"/proc/net/udp", "/proc/net/udp6"} {
		drops, err := scanUDPDropsFile(path, localPort)
		if err != nil {
			return -1, err
		}

		if drops >= 0 {
			return drops, nil
		}
	}

	return -1, nil
}

// scanUDPDropsFile parses one /proc/net/udp[6] file looking for a row
// whose local_address ends with ":<localPort>" (port in big-endian hex)
// and returns the drops column. Returns -1 with no error when the row is
// missing — the caller falls through to the next file.
//
// The file is streamed line-by-line rather than slurped, and a cheap
// substring pre-filter skips the per-field split for the vast majority of
// rows — a busy host's table has dozens of sockets and exactly one can
// match. The pre-filter can false-positive when the port's hex pattern
// appears in another column (remote address, inode); the authoritative
// HasSuffix check on the local_address field runs after the split, so a
// decoy row is never matched.
func scanUDPDropsFile(path string, localPort int) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return -1, nil
		}

		return -1, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only file

	wantSuffix := fmt.Sprintf(":%04X", localPort)
	needle := []byte(wantSuffix)

	scanner := bufio.NewScanner(f)
	first := true

	for scanner.Scan() {
		if first {
			first = false // header row

			continue
		}

		if !bytes.Contains(scanner.Bytes(), needle) {
			continue
		}

		fields := strings.Fields(scanner.Text())
		if len(fields) < 13 {
			continue
		}

		if !strings.HasSuffix(fields[1], wantSuffix) {
			continue
		}

		// drops is the last field on the row.
		drops, parseErr := strconv.ParseInt(fields[len(fields)-1], 10, 64)
		if parseErr != nil {
			return -1, fmt.Errorf("parse drops in %s: %w", path, parseErr)
		}

		return drops, nil
	}

	if scanErr := scanner.Err(); scanErr != nil {
		return -1, fmt.Errorf("scan %s: %w", path, scanErr)
	}

	return -1, nil
}

// getReadBufferBytes returns the kernel's actual SO_RCVBUF for conn. Linux
// reports the doubled value (the kernel adds bookkeeping overhead to
// whatever was requested), so the returned value is typically twice the
// argument passed to SetReadBuffer.
func getReadBufferBytes(conn *net.UDPConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("syscall conn: %w", err)
	}

	var (
		val     int
		sockErr error
	)

	if controlErr := raw.Control(func(fd uintptr) {
		val, sockErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUF)
	}); controlErr != nil {
		return 0, fmt.Errorf("control: %w", controlErr)
	}

	if sockErr != nil {
		return 0, fmt.Errorf("getsockopt SO_RCVBUF: %w", sockErr)
	}

	return val, nil
}

// boolPtrVal dereferences p when non-nil; otherwise returns fallback.
// Used to distinguish "not set" from false in McastPortConfig.Init* fields.
func boolPtrVal(p *bool, fallback bool) bool {
	if p != nil {
		return *p
	}

	return fallback
}

// buildSinglePortChannel opens all sockets and creates the RTP session for one
// McastPortConfig entry. Directions (Send / Receive) are reflected by which
// fields are populated: a Send=false port has nil sender/rtcpSend/rtpSess; a
// Receive=false port has nil receiver. The runtime atomic direction flags are
// initialized from mpc.InitSendEnabled / InitReceiveEnabled (falling back to
// mpc.Send / Receive) so the hot paths can read them without locking.
//
// On any failure the deferred rollback closes whichever sockets and sessions
// were already attached to pc and returns (nil, err) to the caller; the
// individual error sites only need to assign err and bare-return.
func (cfg *CommsConfig) buildSinglePortChannel(
	mpc McastPortConfig,
	localIP string,
	ifi *net.Interface,
	ssrc uint32,
) (pc *PortChannel, err error) {
	pc = &PortChannel{cfg: mpc}
	pc.RxGate.Threshold = cfg.HalfDuplexThreshold
	pc.SendEnabled.Store(boolPtrVal(mpc.InitSendEnabled, mpc.Send))
	pc.ReceiveEnabled.Store(boolPtrVal(mpc.InitReceiveEnabled, mpc.Receive))

	defer func() {
		if err != nil {
			pc.closePartial()
			pc = nil
		}
	}()

	if mpc.Send {
		// ── RTP sender ─────────────────────────────────────────────────
		dst := &net.UDPAddr{IP: net.ParseIP(mpc.Address), Port: mpc.Port}
		src := &net.UDPAddr{IP: net.ParseIP(localIP), Port: 0}

		sendConn, dialErr := net.DialUDP("udp4", src, dst)
		if dialErr != nil {
			err = fmt.Errorf("dial RTP sender %s:%d: %w", mpc.Address, mpc.Port, dialErr)

			return
		}

		pc.Sender = rtp.NewSwappableSender(sendConn)

		if ttlErr := setMulticastTTL(sendConn, rtpMulticastTTL); ttlErr != nil {
			err = fmt.Errorf("set multicast TTL on RTP sender %s:%d: %w", mpc.Address, mpc.Port, ttlErr)

			return
		}

		// The RTP sender's read-back is authoritative for the port
		// snapshot; the TOS byte is stored as its DSCP so the snapshot
		// matches the comms.dscp knob.
		tos, prio := setQoSMarking(sendConn, cfg.DSCP, cfg.Log)
		pc.QoSDSCP.Store(int32(tos >> 2))
		pc.QoSSOPriority.Store(int32(prio))

		// ── RTCP sender ────────────────────────────────────────────────
		rtcpDst := &net.UDPAddr{IP: net.ParseIP(mpc.Address), Port: mpc.Port + 1}
		rtcpSrc := &net.UDPAddr{IP: net.ParseIP(localIP), Port: 0}

		rtcpConn, dialErr := net.DialUDP("udp4", rtcpSrc, rtcpDst)
		if dialErr != nil {
			err = fmt.Errorf("dial RTCP sender %s:%d: %w", mpc.Address, mpc.Port+1, dialErr)

			return
		}

		pc.RTCPSend = rtp.NewSwappableSender(rtcpConn)

		if ttlErr := setMulticastTTL(rtcpConn, rtpMulticastTTL); ttlErr != nil {
			err = fmt.Errorf("set multicast TTL on RTCP sender %s:%d: %w", mpc.Address, mpc.Port+1, ttlErr)

			return
		}

		// RTCP rides the same class as RTP: it is ~1 packet per 5 s, and
		// keeping the pair together avoids reorder-across-classes
		// surprises. The snapshot already records the RTP read-back.
		setQoSMarking(rtcpConn, cfg.DSCP, cfg.Log)

		sess, sessErr := rtp.NewSession(ssrc, pc.Sender, pc.RTCPSend, cfg.Log)
		if sessErr != nil {
			err = fmt.Errorf("pion RTP session for %s:%d: %w", mpc.Address, mpc.Port, sessErr)

			return
		}

		pc.RTPSess = sess

		cfg.Log.Debug().Msgf("comms: RTP sender %s:%d  RTCP %s:%d", mpc.Address, mpc.Port, mpc.Address, mpc.Port+1)
	}

	if mpc.Receive {
		// ── RTP receiver ────────────────────────────────────────────────
		// SO_REUSEPORT lets UpdateMulticastEndpoint open a replacement socket
		// on the same port while the current receiver is still running.
		recvConn, listenErr := listenRTPReceiver(&net.UDPAddr{IP: net.IPv4zero, Port: mpc.Port})
		if listenErr != nil {
			err = fmt.Errorf("listen RTP receiver %s:%d: %w", mpc.Address, mpc.Port, listenErr)

			return
		}

		pc.Receiver = rtp.NewSwappableReceiver(recvConn)
		pc.Jitter = rtp.NewJitterBuffer(rtp.PrebufferPackets, rtp.MaxDepth)

		if bufErr := recvConn.SetReadBuffer(rxSocketBufBytes); bufErr != nil {
			err = fmt.Errorf("set RTP read buffer: %w", bufErr)

			return
		}

		// Verify what the kernel actually granted us. Linux clamps SO_RCVBUF
		// at net.core.rmem_max and silently caps the request, so logging the
		// observed value lets an operator see whether sysctl is undersized
		// for the desired audio safety margin.
		if got, gErr := getReadBufferBytes(recvConn); gErr == nil {
			drops, _ := readUDPSocketDrops(mpc.Port)
			cfg.Log.Debug().
				Int("requested_bytes", rxSocketBufBytes).
				Int("actual_bytes", got).
				Int64("kernel_drops", drops).
				Str("addr", mpc.Address).
				Int("port", mpc.Port).
				Msg("comms: rx socket buffer")
		}

		if joinErr := device.JoinMulticastGroup(ifi, recvConn, net.ParseIP(mpc.Address)); joinErr != nil {
			err = joinErr

			return
		}

		cfg.Log.Debug().Msgf("comms: RTP receiver port %d", mpc.Port)
	}

	return pc, nil
}

// buildNetwork opens sockets for every McastPortConfig entry and returns the
// assembled PortChannel slice plus the local IP address of cfg.Iface. The
// SSRC used for all Send-enabled ports is derived from cfg.RtpID (or
// localIP as fallback), keeping transmissions from this node identifiable
// across talk groups.
func (cfg *CommsConfig) buildNetwork() ([]*PortChannel, string, error) {
	localIP, ifi, err := device.IfaceIPv4(cfg.Iface)
	if err != nil {
		return nil, "", err
	}

	rtpID := cfg.RtpID
	if rtpID == "" {
		rtpID = localIP
	}

	ssrc := rtp.SSRCFromID(rtpID)

	ports := make([]*PortChannel, 0, len(cfg.McastPorts))

	for _, mpc := range cfg.McastPorts {
		pc, err := cfg.buildSinglePortChannel(mpc, localIP, ifi, ssrc)
		if err != nil {
			// Clean up already-built channels before propagating the error.
			for _, built := range ports {
				built.closePartial()
			}

			return nil, "", err
		}

		ports = append(ports, pc)
	}

	return ports, localIP, nil
}

// replaceNetwork atomically swaps the packet-level I/O connections for port 0
// and closes the old connections. newSender, newRTCPSender, or newReceiver may
// be nil when that direction is not applicable to the port. Closing the old
// receiver unblocks any in-flight ReadFromUDP in receiveLoop.
func (cfg *CommsConfig) replaceNetwork(
	rt *CommsRuntime,
	newSender rtp.PacketWriter,
	newRTCPSender rtp.PacketWriter,
	newReceiver rtp.PacketReader,
	newLocalIP string,
) {
	pc := rt.Ports[0]

	if pc.Receiver != nil && newReceiver != nil {
		old := pc.Receiver.Swap(newReceiver)
		_ = old.Close()
	}

	if pc.Sender != nil && newSender != nil {
		// Deferred close: the lock-free Write path on SwappableSender
		// cannot be drained synchronously, so the previous underlying
		// connection is closed after rtp.SwapCloseGrace to let any in-flight
		// sendto(2) on the old fd finish first.
		pc.Sender.SwapAndDeferClose(newSender)
	}

	if pc.RTCPSend != nil && newRTCPSender != nil {
		pc.RTCPSend.SwapAndDeferClose(newRTCPSender)
	}

	rt.LocalIP.Store(&newLocalIP)
}
