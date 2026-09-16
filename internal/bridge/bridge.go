package bridge

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	commsv1 "github.com/openmanet/openmanetd/internal/api/openmanet/comms/v1"
	"github.com/openmanet/openmanetd/internal/api/openmanet/comms/v1/commsv1connect"
	"github.com/openmanet/openmanetd/internal/util/logger"
	"github.com/openmanet/openmanetd/internal/websocket"
	"github.com/rs/zerolog"
)

const (
	// retryBaseDelay is the initial backoff delay for the audio RX loop.
	retryBaseDelay = 2 * time.Second
	// retryMaxDelay is the maximum backoff delay.
	retryMaxDelay = 30 * time.Second
)

// AudioBroadcaster is the subset of Hub used by the bridge for sending audio to clients.
type AudioBroadcaster interface {
	BroadcastAudioRX(channel byte, data []byte)
}

// Bridge connects WebSocket clients to the openmanetd RPC services.
type Bridge struct {
	log      zerolog.Logger
	comms    commsv1connect.CommsServiceClient
	txCtx    context.Context //nolint:containedctx // stored to cancel the persistent TX stream
	hub      *websocket.Hub
	txStream *connect.ClientStreamForClientSimple[commsv1.StreamAudioTxRequest, commsv1.StreamAudioTxResponse]
	txCancel context.CancelFunc
	txMu     sync.Mutex
}

// NewBridge creates a new Bridge.
func NewBridge(hub *websocket.Hub, comms commsv1connect.CommsServiceClient) *Bridge {
	return &Bridge{
		hub:   hub,
		comms: comms,
		log:   logger.GetLogger("bridge"),
	}
}

// HandleMessage is the MessageHandler for the WebSocket Hub. It dispatches
// incoming binary messages from clients to the appropriate RPC call.
func (b *Bridge) HandleMessage(client *websocket.Client, data []byte) {
	if len(data) == 0 {
		return
	}

	ctx := context.Background()
	opcode := data[0]

	switch opcode {
	case websocket.OpcodeAudioTX:
		b.handleAudioTX(ctx, client, data)

	case websocket.OpcodeTXToggle:
		b.handleTXToggle(ctx, client, data)

	case websocket.OpcodeRXToggle:
		b.handleRXToggle(ctx, client, data)

	case websocket.OpcodeRXAllOn:
		client.SetAllRX(true)
		b.log.Debug().Str("remote", client.RemoteAddr()).Msg("RX all on")

	case websocket.OpcodeRXAllOff:
		client.SetAllRX(false)
		b.log.Debug().Str("remote", client.RemoteAddr()).Msg("RX all off")

	case websocket.OpcodeTXAllOn:
		client.SetAllTX(true)
		b.log.Debug().Str("remote", client.RemoteAddr()).Msg("TX all on")

	case websocket.OpcodeTXAllOff:
		client.SetAllTX(false)
		b.log.Debug().Str("remote", client.RemoteAddr()).Msg("TX all off")

	case websocket.OpcodePTTDown:
		b.log.Debug().Str("remote", client.RemoteAddr()).Msg("PTT down")

		if _, err := b.HandleSendPTTEvent(ctx, 1); err != nil {
			b.log.Error().Err(err).Msg("PTT down event failed")
		}

	case websocket.OpcodePTTUp:
		b.log.Debug().Str("remote", client.RemoteAddr()).Msg("PTT up")
		// Close the TX stream before sending PTT up so openmanetd
		// receives all buffered frames before the PTT release.
		b.txMu.Lock()
		b.closeTXStream()
		b.txMu.Unlock()

		if _, err := b.HandleSendPTTEvent(ctx, 0); err != nil {
			b.log.Error().Err(err).Msg("PTT up event failed")
		}

	default:
		b.log.Warn().Uint8("opcode", opcode).Msg("unknown opcode")
	}
}

// getOrOpenTXStream returns the persistent TX stream, opening a new one if
// necessary. Must be called with b.txMu held.
func (b *Bridge) getOrOpenTXStream() (*connect.ClientStreamForClientSimple[commsv1.StreamAudioTxRequest, commsv1.StreamAudioTxResponse], error) {
	if b.txStream != nil {
		return b.txStream, nil
	}

	ctx, cancel := context.WithCancel(context.Background())

	stream, err := b.comms.StreamAudioTx(ctx)
	if err != nil {
		cancel()

		return nil, fmt.Errorf("open TX stream: %w", err)
	}

	b.txCtx = ctx
	b.txCancel = cancel
	b.txStream = stream
	b.log.Info().Msg("audio TX stream opened")

	return stream, nil
}

// closeTXStream closes and discards the current TX stream.
// Must be called with b.txMu held.
func (b *Bridge) closeTXStream() {
	if b.txStream == nil {
		return
	}

	// CloseAndReceive sends the end-of-stream signal and reads the response.
	if _, err := b.txStream.CloseAndReceive(); err != nil {
		b.log.Debug().Err(err).Msg("TX stream close")
	}

	b.txCancel()
	b.txStream = nil
	b.txCtx = nil
	b.txCancel = nil
}

// handleAudioTX processes an audio transmit message from a WebSocket client.
// It extracts the Opus-encoded payload and forwards it to the openmanetd
// WebCommsService via a persistent streaming audio TX RPC call.
func (b *Bridge) handleAudioTX(_ context.Context, client *websocket.Client, data []byte) {
	opusData, err := websocket.ExtractAudioTXPayload(data)
	if err != nil {
		b.log.Error().Err(err).Msg("invalid audio TX message")

		return
	}

	b.txMu.Lock()

	stream, err := b.getOrOpenTXStream()
	if err != nil {
		b.txMu.Unlock()
		b.log.Error().Err(err).Msg("failed to open audio TX stream")

		return
	}

	if err := stream.Send(&commsv1.StreamAudioTxRequest{
		OpusData: opusData,
	}); err != nil {
		b.log.Error().Err(err).Msg("failed to send audio TX frame, resetting stream")
		b.closeTXStream()
		b.txMu.Unlock()

		return
	}

	b.txMu.Unlock()

	// Relay directly to other local WebSocket clients so browsers
	// connected to the same bridge can hear each other without
	// depending on openmanetd's multicast loopback.
	rxBuf := make([]byte, websocket.AudioRXHeaderSize+len(opusData))
	websocket.EncodeAudioRX(rxBuf, websocket.AudioRXMessage{
		Channel:  0,
		OpusData: opusData,
	})
	b.hub.RelayToOthers(client, rxBuf)
}

// handleTXToggle processes a transmit (TX) toggle message from a WebSocket client.
// It decodes the toggle payload and updates both the client's local TX state
// and the remote talk group send state via the communications service RPC.
//
// If the toggle payload cannot be decoded, an error is logged and the function
// returns early without making any changes. If the RPC call to SetSendTalkGroup
// fails, the error is logged along with the affected channel and toggle state.
func (b *Bridge) handleTXToggle(ctx context.Context, client *websocket.Client, data []byte) {
	toggle, err := websocket.DecodeToggle(data)
	if err != nil {
		b.log.Error().Err(err).Msg("invalid TX toggle")

		return
	}

	client.SetTX(toggle.Channel, toggle.On)

	_, err = b.comms.SetSendTalkGroup(ctx, &commsv1.SetSendTalkGroupRequest{
		Talkgroup: int32(toggle.Channel),
		Enabled:   toggle.On,
	})
	if err != nil {
		b.log.Error().Err(err).Int("channel", int(toggle.Channel)).Bool("on", toggle.On).Msg("SetSendTalkGroup RPC failed")
	}
}

// handleRXToggle processes a receive (RX) toggle message from a WebSocket client.
// It decodes the toggle payload and updates both the client's local RX state
// and the remote talk group receive state via the communications service RPC.
//
// Parameters:
//   - ctx: The context for managing the lifecycle of the RPC call.
//   - client: The WebSocket client that sent the toggle message.
//   - data: The raw bytes containing the encoded toggle payload.
//
// If the toggle payload cannot be decoded, an error is logged and the function
// returns early without making any changes. If the RPC call to SetReceiveTalkGroup
// fails, the error is logged along with the affected channel and toggle state.
func (b *Bridge) handleRXToggle(ctx context.Context, client *websocket.Client, data []byte) {
	toggle, err := websocket.DecodeToggle(data)
	if err != nil {
		b.log.Error().Err(err).Msg("invalid RX toggle")

		return
	}

	client.SetRX(toggle.Channel, toggle.On)

	_, err = b.comms.SetReceiveTalkGroup(ctx, &commsv1.SetReceiveTalkGroupRequest{
		Talkgroup: int32(toggle.Channel),
		Enabled:   toggle.On,
	})
	if err != nil {
		b.log.Error().Err(err).Int("channel", int(toggle.Channel)).Bool("on", toggle.On).Msg("SetReceiveTalkGroup RPC failed")
	}
}

// StartAudioRXLoop starts a long-running goroutine that receives audio frames
// from the openmanetd WebCommsService and broadcasts them to subscribed WebSocket clients.
func (b *Bridge) StartAudioRXLoop(ctx context.Context) {
	go func() {
		delay := retryBaseDelay

		for {
			err := b.receiveAudioRX(ctx)
			if err != nil {
				if isExpectedPrecondition(err) {
					b.log.Trace().Err(err).Msgf("audio RX not ready, retrying in %s", delay)
				} else {
					b.log.Error().Err(err).Msgf("audio RX stream ended, retrying in %s", delay)
				}
			}

			// Always reset delay — the stream connected successfully
			// if we got past the StreamAudioRx call.
			if err == nil || !isExpectedPrecondition(err) {
				delay = retryBaseDelay
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}

			// Exponential backoff capped at retryMaxDelay.
			delay = min(delay*2, retryMaxDelay)
		}
	}()
}

// isExpectedPrecondition returns true if the error indicates the openmanetd
// web audio bridge is not yet active — a normal state at startup.
func isExpectedPrecondition(err error) bool {
	return err != nil && strings.Contains(err.Error(), "failed_precondition")
}

// wsChannel maps the talkgroup carried on a StreamAudioRx frame to the
// WebSocket channel byte. Zero (a daemon that predates the talkgroup
// field) and out-of-range values fall back to channel 1 so audio keeps
// flowing to clients with the default channel enabled.
func wsChannel(talkgroup int32) byte {
	if talkgroup < 1 || talkgroup > 255 {
		return 1
	}

	return byte(talkgroup)
}

// receiveAudioRX opens a streaming RPC to receive audio frames from the
// openmanetd WebCommsService, encodes each frame into the WebSocket binary
// protocol, and broadcasts them to subscribed clients via the Hub.
// It returns an error when the stream ends or encounters a fatal failure.
func (b *Bridge) receiveAudioRX(ctx context.Context) error {
	b.log.Debug().Msg("opening audio RX stream...")

	stream, err := b.comms.StreamAudioRx(ctx, &commsv1.StreamAudioRxRequest{})
	if err != nil {
		return fmt.Errorf("StreamAudioRx: %w", err)
	}
	defer stream.Close()

	b.log.Debug().Msg("audio RX stream connected")

	var frameCount uint64

	for stream.Receive() {
		frame := stream.Msg()
		if frame == nil || len(frame.OpusData) == 0 {
			continue
		}

		frameCount++
		if frameCount == 1 {
			b.log.Info().
				Uint32("seq", frame.Sequence).
				Int("opus_bytes", len(frame.OpusData)).
				Msg("first audio RX frame received")
		} else if frameCount%500 == 0 {
			b.log.Debug().
				Uint64("total_frames", frameCount).
				Uint32("seq", frame.Sequence).
				Int("clients", b.hub.ClientCount()).
				Msg("audio RX progress")
		}

		// The WS binary protocol adds channel/SSRC/srcIP. The channel is
		// the talkgroup the daemon received the frame on — the hub uses
		// it to honor per-channel RX toggles and the UI uses it to
		// attribute RX activity. SSRC/srcIP are not in the protobuf and
		// remain zero.
		msg := websocket.AudioRXMessage{
			Channel:  wsChannel(frame.Talkgroup),
			Seq:      uint16(frame.Sequence),
			OpusData: frame.OpusData,
		}

		buf := make([]byte, websocket.AudioRXHeaderSize+len(frame.OpusData))
		websocket.EncodeAudioRX(buf, msg)

		b.hub.BroadcastAudioRX(msg.Channel, buf)
	}

	b.log.Debug().Uint64("total_frames", frameCount).Msg("audio RX stream ended")

	if err := stream.Err(); err != nil {
		return fmt.Errorf("audio RX stream: %w", err)
	}

	return nil
}

// HandleSendPTTEvent forwards a PTT event to the openmanetd service.
func (b *Bridge) HandleSendPTTEvent(ctx context.Context, event int32) (*commsv1.SendPTTEventResponse, error) {
	resp, err := b.comms.SendPTTEvent(ctx, &commsv1.SendPTTEventRequest{
		Event: event,
	})
	if err != nil {
		return nil, fmt.Errorf("SendPTTEvent: %w", err)
	}

	return resp, nil
}
