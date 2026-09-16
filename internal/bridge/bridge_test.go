package bridge

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	commsv1 "github.com/openmanet/openmanetd/internal/api/openmanet/comms/v1"
	"github.com/openmanet/openmanetd/internal/websocket"
	"google.golang.org/protobuf/types/known/emptypb"
)

// mockCommsClient records calls to the comms RPC service.
type mockCommsClient struct {
	setSendCalls         []setSendCall
	setReceiveCalls      []setReceiveCall
	selectTalkGroupCalls []int32
	pttCalls             []int32
	statusResp           *commsv1.GetCommsStatusResponse
}

type setSendCall struct {
	talkgroup int32
	enabled   bool
}

type setReceiveCall struct {
	talkgroup int32
	enabled   bool
}

func (m *mockCommsClient) GetCommsConfig(_ context.Context, _ *emptypb.Empty) (*commsv1.GetCommsConfigResponse, error) {
	return &commsv1.GetCommsConfigResponse{}, nil
}

func (m *mockCommsClient) UpdateCommsConfig(_ context.Context, _ *commsv1.UpdateCommsConfigRequest) (*commsv1.UpdateCommsConfigResponse, error) {
	return &commsv1.UpdateCommsConfigResponse{}, nil
}

func (m *mockCommsClient) GetCommsStatus(_ context.Context, _ *emptypb.Empty) (*commsv1.GetCommsStatusResponse, error) {
	return m.statusResp, nil
}

func (m *mockCommsClient) SetSendTalkGroup(_ context.Context, req *commsv1.SetSendTalkGroupRequest) (*commsv1.SetSendTalkGroupResponse, error) {
	m.setSendCalls = append(m.setSendCalls, setSendCall{req.Talkgroup, req.Enabled})

	return &commsv1.SetSendTalkGroupResponse{Success: true}, nil
}

func (m *mockCommsClient) SetReceiveTalkGroup(_ context.Context, req *commsv1.SetReceiveTalkGroupRequest) (*commsv1.SetReceiveTalkGroupResponse, error) {
	m.setReceiveCalls = append(m.setReceiveCalls, setReceiveCall{req.Talkgroup, req.Enabled})

	return &commsv1.SetReceiveTalkGroupResponse{Success: true}, nil
}

func (m *mockCommsClient) SelectTalkGroup(_ context.Context, req *commsv1.SelectTalkGroupRequest) (*commsv1.SelectTalkGroupResponse, error) {
	m.selectTalkGroupCalls = append(m.selectTalkGroupCalls, req.Talkgroup)

	return &commsv1.SelectTalkGroupResponse{Success: true}, nil
}

func (m *mockCommsClient) StreamTalkGroupEvents(_ context.Context, _ *emptypb.Empty) (*connect.ServerStreamForClient[commsv1.StreamTalkGroupEventsResponse], error) {
	return nil, nil
}

func (m *mockCommsClient) SendPTTEvent(_ context.Context, req *commsv1.SendPTTEventRequest) (*commsv1.SendPTTEventResponse, error) {
	m.pttCalls = append(m.pttCalls, req.Event)

	return &commsv1.SendPTTEventResponse{Success: true}, nil
}

func (m *mockCommsClient) StreamAudioTx(_ context.Context) (*connect.ClientStreamForClientSimple[commsv1.StreamAudioTxRequest, commsv1.StreamAudioTxResponse], error) {
	return nil, nil
}

func (m *mockCommsClient) StreamAudioRx(_ context.Context, _ *commsv1.StreamAudioRxRequest) (*connect.ServerStreamForClient[commsv1.StreamAudioRxResponse], error) {
	return nil, nil
}

func (m *mockCommsClient) GetAudioMixer(_ context.Context, _ *emptypb.Empty) (*commsv1.GetAudioMixerResponse, error) {
	return &commsv1.GetAudioMixerResponse{}, nil
}

func (m *mockCommsClient) UpdateAudioMixer(_ context.Context, _ *commsv1.UpdateAudioMixerRequest) (*commsv1.UpdateAudioMixerResponse, error) {
	return &commsv1.UpdateAudioMixerResponse{}, nil
}

func newTestBridge() (*Bridge, *mockCommsClient) {
	comms := &mockCommsClient{}
	hub := websocket.NewHub(nil)
	b := NewBridge(hub, comms)

	return b, comms
}

// TestWSChannelForTalkgroup pins the talkgroup→WS-channel-byte mapping used
// by the audio RX loop. Zero (a daemon that predates the talkgroup field)
// and out-of-range values fall back to channel 1 so audio keeps flowing.
func TestWSChannelForTalkgroup(t *testing.T) {
	tests := []struct {
		name      string
		talkgroup int32
		want      byte
	}{
		{name: "zero falls back to 1", talkgroup: 0, want: 1},
		{name: "channel 1", talkgroup: 1, want: 1},
		{name: "channel 2", talkgroup: 2, want: 2},
		{name: "channel 5", talkgroup: 5, want: 5},
		{name: "max byte", talkgroup: 255, want: 255},
		{name: "above byte range falls back", talkgroup: 256, want: 1},
		{name: "negative falls back", talkgroup: -3, want: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := wsChannel(tc.talkgroup); got != tc.want {
				t.Errorf("wsChannel(%d): got %d, want %d", tc.talkgroup, got, tc.want)
			}
		})
	}
}

func TestBridge_HandleTXToggle(t *testing.T) {
	b, comms := newTestBridge()

	client := &websocket.Client{}

	// TX toggle on for channel 2.
	data := []byte{websocket.OpcodeTXToggle, 2, 1}
	b.HandleMessage(client, data)

	if len(comms.setSendCalls) != 1 {
		t.Fatalf("expected 1 SetSendTalkGroup call, got %d", len(comms.setSendCalls))
	}

	if comms.setSendCalls[0].talkgroup != 2 {
		t.Errorf("talkgroup = %d, want 2", comms.setSendCalls[0].talkgroup)
	}

	if !comms.setSendCalls[0].enabled {
		t.Error("enabled = false, want true")
	}
}

func TestBridge_HandleRXToggle(t *testing.T) {
	b, comms := newTestBridge()

	client := &websocket.Client{}

	// RX toggle off for channel 5.
	data := []byte{websocket.OpcodeRXToggle, 5, 0}
	b.HandleMessage(client, data)

	if len(comms.setReceiveCalls) != 1 {
		t.Fatalf("expected 1 SetReceiveTalkGroup call, got %d", len(comms.setReceiveCalls))
	}

	if comms.setReceiveCalls[0].talkgroup != 5 {
		t.Errorf("talkgroup = %d, want 5", comms.setReceiveCalls[0].talkgroup)
	}

	if comms.setReceiveCalls[0].enabled {
		t.Error("enabled = true, want false")
	}
}

func TestBridge_HandleRXAllOn(t *testing.T) {
	b, _ := newTestBridge()
	client := &websocket.Client{}

	data := []byte{websocket.OpcodeRXAllOn}
	b.HandleMessage(client, data)

	// Verify client state.
	for i := byte(0); i < websocket.MaxChannels; i++ {
		if !client.IsRXEnabled(i) {
			t.Errorf("channel %d RX should be enabled", i)
		}
	}
}

func TestBridge_HandleRXAllOff(t *testing.T) {
	b, _ := newTestBridge()
	client := &websocket.Client{}
	client.SetAllRX(true)

	data := []byte{websocket.OpcodeRXAllOff}
	b.HandleMessage(client, data)

	for i := byte(0); i < websocket.MaxChannels; i++ {
		if client.IsRXEnabled(i) {
			t.Errorf("channel %d RX should be disabled", i)
		}
	}
}

func TestBridge_HandleTXAllOn(t *testing.T) {
	b, _ := newTestBridge()
	client := &websocket.Client{}

	data := []byte{websocket.OpcodeTXAllOn}
	b.HandleMessage(client, data)
	// TX all on should not panic; TX state is internal to client.
}

func TestBridge_HandleTXAllOff(t *testing.T) {
	b, _ := newTestBridge()
	client := &websocket.Client{}

	data := []byte{websocket.OpcodeTXAllOff}
	b.HandleMessage(client, data)
	// TX all off should not panic.
}

func TestBridge_HandleEmptyMessage(t *testing.T) {
	b, _ := newTestBridge()
	client := &websocket.Client{}

	// Should not panic.
	b.HandleMessage(client, nil)
	b.HandleMessage(client, []byte{})
}

func TestBridge_HandleUnknownOpcode(t *testing.T) {
	b, _ := newTestBridge()
	client := &websocket.Client{}

	// Should not panic.
	b.HandleMessage(client, []byte{0xFF})
}

func TestBridge_HandleSendPTTEvent(t *testing.T) {
	b, comms := newTestBridge()

	resp, err := b.HandleSendPTTEvent(context.Background(), 1)
	if err != nil {
		t.Fatalf("HandleSendPTTEvent() error: %v", err)
	}

	if !resp.Success {
		t.Error("Success = false, want true")
	}

	if len(comms.pttCalls) != 1 || comms.pttCalls[0] != 1 {
		t.Errorf("expected PTT event 1, got %v", comms.pttCalls)
	}
}
