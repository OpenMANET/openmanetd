package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	commsv1 "github.com/openmanet/openmanetd/internal/api/openmanet/comms/v1"
	"github.com/openmanet/openmanetd/internal/comms"
	"github.com/openmanet/openmanetd/internal/comms/control"
	"github.com/openmanet/openmanetd/internal/comms/control/alsa"
	"github.com/openmanet/openmanetd/internal/comms/talkgroup"
	"github.com/openmanet/openmanetd/internal/config"
)

// AudioMixer is the hardware mixer surface the comms handler needs.
// Production wiring passes *alsa.Volume; tests supply fakeAudioMixer.
type AudioMixer interface {
	State(ctx context.Context) (alsa.State, error)
	Apply(ctx context.Context, u alsa.Update) (alsa.State, error)
}

// CommsService implements the commsv1connect.CommsServiceHandler interface,
// combining configuration, status, talkgroup control, PTT events, and audio
// streaming into a single handler.
//
// Service is a lazy accessor that returns the live *comms.Service most
// recently published by the comms subsystem (or nil if comms has not been
// started). It is a function so the handler can be constructed at startup
// before CommsManager.Enable runs and still resolve the freshly built
// service on each request. Production wiring passes comms.Default; tests
// supply a closure that returns a hand-built *comms.Service or nil.
type CommsService struct {
	Cfg          *config.Config
	Log          zerolog.Logger
	CommsManager comms.CommsLifecycle
	Service      func() *comms.Service
	Mixer        AudioMixer

	// mu serializes UpdateAudioMixer: the hardware write and the config
	// persist must not interleave between concurrent requests.
	mu sync.Mutex
}

// commsService returns the live *comms.Service via the injected accessor,
// or nil if no accessor has been wired (defensive — production always
// supplies comms.Default).
func (c *CommsService) commsService() *comms.Service {
	if c.Service == nil {
		return nil
	}

	return c.Service()
}

// controlSourceToProto maps a config string to the proto ControlSource enum.
func controlSourceToProto(src string) commsv1.ControlSource {
	switch strings.ToLower(src) {
	case "nanoptt":
		return commsv1.ControlSource_CONTROL_SOURCE_NANOPTT
	case "web":
		return commsv1.ControlSource_CONTROL_SOURCE_WEB
	default:
		return commsv1.ControlSource_CONTROL_SOURCE_OPENVLM
	}
}

// protoToControlSource maps a proto ControlSource enum to the config string.
func protoToControlSource(src commsv1.ControlSource) string {
	switch src {
	case commsv1.ControlSource_CONTROL_SOURCE_NANOPTT:
		return "nanoptt"
	case commsv1.ControlSource_CONTROL_SOURCE_WEB:
		return "web"
	case commsv1.ControlSource_CONTROL_SOURCE_OPENVLM:
		return "openvlm"
	default:
		return "openvlm"
	}
}

// GetCommsConfig retrieves the current communications configuration.
func (c *CommsService) GetCommsConfig(_ context.Context, _ *emptypb.Empty) (*commsv1.GetCommsConfigResponse, error) {
	return &commsv1.GetCommsConfigResponse{
		CommsEnabled:  c.Cfg.GetCommsEnable(),
		ControlSource: controlSourceToProto(c.Cfg.GetCommsControlSource()),
	}, nil
}

// UpdateCommsConfig updates the EnableComms and ControlSource settings,
// persisting them to the config file and applying changes at runtime.
func (c *CommsService) UpdateCommsConfig(_ context.Context, req *commsv1.UpdateCommsConfigRequest) (*commsv1.UpdateCommsConfigResponse, error) {
	ctrlSrc := protoToControlSource(req.GetControlSource())

	if err := c.Cfg.PersistCommsConfig(req.GetEnableComms(), ctrlSrc); err != nil {
		c.Log.Error().Err(err).Msg("Failed to persist comms config")

		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to persist comms config: %w", err))
	}

	// Apply runtime changes via the CommsManager.
	if c.CommsManager != nil {
		// Always stop current instance first (handles control source swap).
		c.CommsManager.Disable()

		if req.GetEnableComms() {
			if err := c.CommsManager.Enable(); err != nil {
				c.Log.Error().Err(err).Msg("Failed to enable comms at runtime")

				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to enable comms: %w", err))
			}
		}
	}

	return &commsv1.UpdateCommsConfigResponse{}, nil
}

// GetCommsStatus retrieves the current status of the communications service.
// It returns the active talk group and a list of all available talk groups.
func (c *CommsService) GetCommsStatus(_ context.Context, _ *emptypb.Empty) (*commsv1.GetCommsStatusResponse, error) {
	if !c.Cfg.GetCommsEnable() {
		return nil, errors.New("comms module not enabled")
	}

	availableTalkGroups := config.GetMulticastTalkGroups()

	talkGroupProtos := make([]int32, len(availableTalkGroups))
	for i, tg := range availableTalkGroups {
		channel, err := config.TalkGroupChannel(tg.Port)
		if err != nil {
			return nil, err
		}

		talkGroupProtos[i] = int32(channel)
	}

	svc := c.commsService()
	activeTalkGroupPort := svc.ActiveMulticastPort()

	var activeTalkGroupChannel int32

	if activeTalkGroupPort != 0 {
		ch, err := config.TalkGroupChannel(activeTalkGroupPort)
		if err != nil {
			return nil, err
		}

		activeTalkGroupChannel = int32(ch)
	}

	var (
		codec   string
		ptimeMs int32
	)

	if c.Cfg.GetCommsEnable() {
		codec = fmt.Sprintf("OPUS %dK", comms.TargetBitrate/1000)
		// Opus frame duration in ms. Sourced from the fixed audiopool frame
		// size (960 samples at 48 kHz = 20 ms); if the encoder ever exposes
		// a variable frame size, read it from a snapshot instead.
		ptimeMs = 20
	}

	return &commsv1.GetCommsStatusResponse{
		ActiveTalkgroup:     activeTalkGroupChannel,
		AvailableTalkgroups: talkGroupProtos,
		TalkgroupStates:     buildTalkGroupStates(svc),
		Codec:               codec,
		PtimeMs:             ptimeMs,
		// TODO(api-plan): populate round_trip_ms from RTCP SR/RR once the
		// RTP session tracks receiver reports.
		RoundTripMs: 0,
	}, nil
}

// buildTalkGroupStates returns a proto slice of TalkGroupState by querying the
// supplied service. It returns nil (not an error) when svc is nil or comms
// is not running so that GetCommsStatus remains usable even when the
// subsystem is disabled.
func buildTalkGroupStates(svc *comms.Service) []*commsv1.TalkGroupState {
	states, err := svc.TalkGroupStates()
	if err != nil {
		return nil
	}

	result := make([]*commsv1.TalkGroupState, 0, len(states))

	for _, s := range states {
		ch, chErr := config.TalkGroupChannel(s.Port)
		if chErr != nil {
			continue // skip ports that don't map to a channel
		}

		result = append(result, &commsv1.TalkGroupState{
			Talkgroup:      int32(ch),
			Address:        s.Address,
			Port:           int32(s.Port),
			SendEnabled:    s.SendEnabled,
			ReceiveEnabled: s.ReceiveEnabled,
		})
	}

	return result
}

// talkGroupPortIdx resolves a 1-based channel number to the zero-based port
// index used by Service.EnableTalkGroupSend / Service.EnableTalkGroupReceive.
func talkGroupPortIdx(svc *comms.Service, channel int) (int, error) {
	targetPort, err := config.TalkGroupPort(channel)
	if err != nil {
		return 0, err
	}

	states, err := svc.TalkGroupStates()
	if err != nil {
		return 0, err
	}

	for i, s := range states {
		if s.Port == targetPort {
			return i, nil
		}
	}

	return 0, errors.New("talkgroup channel not found in active comms configuration")
}

// SetSendTalkGroup enables or disables RTP transmission on the talkgroup
// identified by the 1-based channel number in the request.
func (c *CommsService) SetSendTalkGroup(_ context.Context, req *commsv1.SetSendTalkGroupRequest) (*commsv1.SetSendTalkGroupResponse, error) {
	if !c.Cfg.GetCommsEnable() {
		return nil, errors.New("comms module not enabled")
	}

	svc := c.commsService()

	portIdx, err := talkGroupPortIdx(svc, int(req.GetTalkgroup()))
	if err != nil {
		return &commsv1.SetSendTalkGroupResponse{
			Success: false,
			Message: err.Error(),
		}, err
	}

	if err := svc.EnableTalkGroupSend(portIdx, req.GetEnabled()); err != nil {
		return &commsv1.SetSendTalkGroupResponse{
			Success: false,
			Message: err.Error(),
		}, err
	}

	return &commsv1.SetSendTalkGroupResponse{
		Success: true,
	}, nil
}

// SetReceiveTalkGroup enables or disables RTP reception on the talkgroup
// identified by the 1-based channel number in the request.
func (c *CommsService) SetReceiveTalkGroup(_ context.Context, req *commsv1.SetReceiveTalkGroupRequest) (*commsv1.SetReceiveTalkGroupResponse, error) {
	if !c.Cfg.GetCommsEnable() {
		return nil, errors.New("comms module not enabled")
	}

	svc := c.commsService()

	portIdx, err := talkGroupPortIdx(svc, int(req.GetTalkgroup()))
	if err != nil {
		return &commsv1.SetReceiveTalkGroupResponse{
			Success: false,
			Message: err.Error(),
		}, err
	}

	if err := svc.EnableTalkGroupReceive(portIdx, req.GetEnabled()); err != nil {
		return &commsv1.SetReceiveTalkGroupResponse{
			Success: false,
			Message: err.Error(),
		}, err
	}

	return &commsv1.SetReceiveTalkGroupResponse{
		Success: true,
	}, nil
}

// talkGroupEventChanSize bounds the per-subscriber event buffer. 16
// absorbs the burst a rapid selector spin can produce while keeping
// per-stream memory trivial; overflow drops (counted via NoteDropped)
// rather than blocking the notifier.
const talkGroupEventChanSize = 16

// SelectTalkGroup makes the requested talk group the single active
// channel via the comms selection spine.
func (c *CommsService) SelectTalkGroup(_ context.Context, req *commsv1.SelectTalkGroupRequest) (*commsv1.SelectTalkGroupResponse, error) {
	if !c.Cfg.GetCommsEnable() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("comms module not enabled"))
	}

	if err := c.commsService().SelectTalkGroup(int(req.GetTalkgroup()), talkgroup.SourceRPC); err != nil {
		if errors.Is(err, comms.ErrNotRunning) {
			c.Log.Error().Err(err).Msg("SelectTalkGroup: comms not running")

			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}

		c.Log.Warn().Err(err).Int32("talkgroup", req.GetTalkgroup()).Msg("SelectTalkGroup rejected")

		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	return &commsv1.SelectTalkGroupResponse{Success: true}, nil
}

// StreamTalkGroupEvents streams talk group change events to the client.
// Near-copy of the BLOS event stream: bounded channel, non-blocking send
// with drop accounting, listener removed on return.
func (c *CommsService) StreamTalkGroupEvents(ctx context.Context, _ *emptypb.Empty, stream *connect.ServerStream[commsv1.StreamTalkGroupEventsResponse]) error {
	reg := c.commsService().Events()
	if reg == nil {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("comms subsystem is not running"))
	}

	ch := make(chan *commsv1.TalkGroupEvent, talkGroupEventChanSize)

	id := reg.Add(TalkGroupEventListener(ch, reg))
	defer reg.Remove(id)

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-ch:
			if !ok {
				return nil
			}

			if err := stream.Send(&commsv1.StreamTalkGroupEventsResponse{Event: ev}); err != nil {
				return err
			}
		}
	}
}

// TalkGroupEventToProto converts an internal talk group event to its
// wire form. Enum numeric values are aligned by construction (the proto
// enums mirror talkgroup.Kind / talkgroup.Source), so direct casts are
// safe; TestTalkGroupEventToProto pins the alignment.
// TalkGroupEventListener returns the registry listener that feeds one
// stream subscriber's bounded channel. Non-blocking by the registry's
// contract: when ch is full the newest event is dropped and counted via
// NoteDropped, never stalling the notifier. Exported so the overflow
// branch is unit-testable (same rationale as TalkGroupEventToProto).
func TalkGroupEventListener(ch chan<- *commsv1.TalkGroupEvent, reg *talkgroup.Registry) func(talkgroup.Event) {
	return func(ev talkgroup.Event) {
		select {
		case ch <- TalkGroupEventToProto(ev):
		default:
			reg.NoteDropped()
		}
	}
}

func TalkGroupEventToProto(ev talkgroup.Event) *commsv1.TalkGroupEvent {
	return &commsv1.TalkGroupEvent{
		Kind:           commsv1.TalkGroupEventKind(ev.Kind),
		Talkgroup:      int32(ev.Channel),
		PrevTalkgroup:  int32(ev.Prev),
		SendEnabled:    ev.Send,
		ReceiveEnabled: ev.Receive,
		Source:         commsv1.TalkGroupEventSource(ev.Source),
		At:             timestamppb.New(ev.At),
	}
}

// SendPTTEvent delivers a PTT state change from the web client to the comms
// event loop. Returns FailedPrecondition when the web control source is not
// active.
func (c *CommsService) SendPTTEvent(_ context.Context, req *commsv1.SendPTTEventRequest) (*commsv1.SendPTTEventResponse, error) {
	ws := c.commsService().WebEventSource()
	if ws == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("web control source not active"))
	}

	var ev control.PTTEvent

	switch req.GetEvent() {
	case 0:
		ev = control.PTTDown
	case 1:
		ev = control.PTTUp
	case 2:
		ev = control.PTTToggle
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid PTT event type"))
	}

	ws.Push(ev)

	return &commsv1.SendPTTEventResponse{Success: true}, nil
}

// StreamAudioTx receives a stream of Opus-encoded audio frames from the web
// client and injects them into the RTP send path. Returns FailedPrecondition
// when the web audio bridge is not active.
func (c *CommsService) StreamAudioTx(_ context.Context, stream *connect.ClientStream[commsv1.StreamAudioTxRequest]) (*commsv1.StreamAudioTxResponse, error) {
	bridge := c.commsService().WebAudioBridge()
	if bridge == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("web audio bridge not active"))
	}

	var count uint32

	for stream.Receive() {
		msg := stream.Msg()
		bridge.InjectTxFrame(msg.GetOpusData())

		count++
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}

	c.Log.Debug().Uint32("frames_received", count).Msg("Received audio frames from web client")

	return &commsv1.StreamAudioTxResponse{FramesReceived: count}, nil
}

// StreamAudioRx streams Opus-encoded audio frames received from the mesh
// back to the web client. Returns FailedPrecondition when the web audio
// bridge is not active.
func (c *CommsService) StreamAudioRx(ctx context.Context, _ *commsv1.StreamAudioRxRequest, stream *connect.ServerStream[commsv1.StreamAudioRxResponse]) error {
	bridge := c.commsService().WebAudioBridge()
	if bridge == nil {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("web audio bridge not active"))
	}

	// Register as a consumer so the comms-side playout drain starts doing
	// per-frame work; with no stream attached it discards frames without
	// copying or touching the channel.
	bridge.AddConsumer()
	defer bridge.RemoveConsumer()

	var seq uint32

	for {
		select {
		case <-ctx.Done():
			return nil
		case frame, ok := <-bridge.RxFrames():
			if !ok {
				return nil
			}

			seq++

			// Release after Send: the proto marshal copies the payload
			// into the wire buffer synchronously, so the pooled frame
			// buffer is free to recycle once Send returns.
			err := stream.Send(&commsv1.StreamAudioRxResponse{
				OpusData:  frame.Data(),
				Sequence:  seq,
				Talkgroup: int32(frame.Channel()),
			})

			frame.Release()

			if err != nil {
				return err
			}

			c.Log.Trace().Uint32("seq", seq).Msg("Sent audio frame to web client")
		}
	}
}

// audioMixerStateToProto maps an alsa.State onto the wire message.
// Optional fields are set only when the backing control exists.
func audioMixerStateToProto(st alsa.State) *commsv1.AudioMixerState {
	out := &commsv1.AudioMixerState{
		Available:      st.Available,
		SpeakerControl: st.SpeakerControl,
		MicControl:     st.MicControl,
		AgcControl:     st.AGCControl,
	}

	if st.SpeakerPct >= 0 {
		v := int32(st.SpeakerPct)
		out.SpeakerVolume = &v
	}

	if st.MicPct >= 0 {
		v := int32(st.MicPct)
		out.MicVolume = &v
	}

	if st.AGCPresent {
		v := st.AGCEnabled
		out.AgcEnabled = &v
	}

	return out
}

// GetAudioMixer reads the device's hardware mixer state. A missing sound
// card is reported as available=false, not as an error, so the UI can
// render a disabled panel.
func (c *CommsService) GetAudioMixer(ctx context.Context, _ *emptypb.Empty) (*commsv1.GetAudioMixerResponse, error) {
	if c.Mixer == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("audio mixer not available"))
	}

	st, err := c.Mixer.State(ctx)
	if err != nil {
		if errors.Is(err, alsa.ErrNoCard) {
			return &commsv1.GetAudioMixerResponse{State: &commsv1.AudioMixerState{}}, nil
		}

		c.Log.Error().Err(err).Msg("Failed to read audio mixer state")

		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("read mixer state: %w", err))
	}

	return &commsv1.GetAudioMixerResponse{State: audioMixerStateToProto(st)}, nil
}

// UpdateAudioMixer applies the provided fields to hardware first, then
// persists volumes and AGC to the config file. Hardware-before-persist
// ordering means a failed persist can never leave config claiming a level
// the hardware rejected.
func (c *CommsService) UpdateAudioMixer(ctx context.Context, req *commsv1.UpdateAudioMixerRequest) (*commsv1.UpdateAudioMixerResponse, error) {
	if c.Mixer == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("audio mixer not available"))
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	var (
		u       alsa.Update
		speaker *int
		mic     *int
		agc     *bool
	)

	if req.SpeakerVolume != nil {
		v := int(req.GetSpeakerVolume())
		u.SpeakerPct = &v
		speaker = &v
	}

	if req.MicVolume != nil {
		v := int(req.GetMicVolume())
		u.MicPct = &v
		mic = &v
	}

	if req.AgcEnabled != nil {
		v := req.GetAgcEnabled()
		u.AGC = &v
		agc = &v
	}

	st, err := c.Mixer.Apply(ctx, u)
	if err != nil {
		if errors.Is(err, alsa.ErrNoCard) || errors.Is(err, alsa.ErrControlNotFound) {
			c.Log.Warn().Err(err).Msg("Audio mixer update rejected")

			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}

		c.Log.Error().Err(err).Msg("Failed to apply audio mixer update")

		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("apply mixer update: %w", err))
	}

	if speaker != nil || mic != nil || agc != nil {
		if pErr := c.Cfg.PersistCommsAudio(speaker, mic, agc); pErr != nil {
			c.Log.Error().Err(pErr).Msg("Failed to persist comms audio config")

			return nil, connect.NewError(connect.CodeInternal,
				fmt.Errorf("mixer applied to hardware but persisting failed: %w", pErr))
		}
	}

	return &commsv1.UpdateAudioMixerResponse{State: audioMixerStateToProto(st)}, nil
}
