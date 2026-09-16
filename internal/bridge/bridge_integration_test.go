//go:build integration

package bridge_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/gorilla/websocket"
	commsv1 "github.com/openmanet/openmanetd/internal/api/openmanet/comms/v1"
	"github.com/openmanet/openmanetd/internal/api/openmanet/comms/v1/commsv1connect"
	"github.com/openmanet/openmanetd/internal/bridge"
	ws "github.com/openmanet/openmanetd/internal/websocket"
	"google.golang.org/protobuf/types/known/emptypb"
)

// integrationCommsHandler tracks RPC calls received from the bridge so the
// test can assert that WebSocket events are translated into the expected
// upstream ConnectRPC calls.
type integrationCommsHandler struct {
	commsv1connect.UnimplementedCommsServiceHandler

	sendCalls    chan *commsv1.SetSendTalkGroupRequest
	receiveCalls chan *commsv1.SetReceiveTalkGroupRequest
	pttCalls     chan *commsv1.SendPTTEventRequest
}

func (h *integrationCommsHandler) GetCommsStatus(_ context.Context, _ *emptypb.Empty) (*commsv1.GetCommsStatusResponse, error) {
	return &commsv1.GetCommsStatusResponse{}, nil
}

func (h *integrationCommsHandler) SetSendTalkGroup(_ context.Context, req *commsv1.SetSendTalkGroupRequest) (*commsv1.SetSendTalkGroupResponse, error) {
	h.sendCalls <- req

	return &commsv1.SetSendTalkGroupResponse{Success: true}, nil
}

func (h *integrationCommsHandler) SetReceiveTalkGroup(_ context.Context, req *commsv1.SetReceiveTalkGroupRequest) (*commsv1.SetReceiveTalkGroupResponse, error) {
	h.receiveCalls <- req

	return &commsv1.SetReceiveTalkGroupResponse{Success: true}, nil
}

func (h *integrationCommsHandler) SendPTTEvent(_ context.Context, req *commsv1.SendPTTEventRequest) (*commsv1.SendPTTEventResponse, error) {
	h.pttCalls <- req

	return &commsv1.SendPTTEventResponse{Success: true}, nil
}

func TestIntegration_WSClientToRPC(t *testing.T) {
	// Stand up a real ConnectRPC server hosting the CommsService so the
	// bridge exercises its full client→server code path.
	commsHandler := &integrationCommsHandler{
		sendCalls:    make(chan *commsv1.SetSendTalkGroupRequest, 10),
		receiveCalls: make(chan *commsv1.SetReceiveTalkGroupRequest, 10),
		pttCalls:     make(chan *commsv1.SendPTTEventRequest, 10),
	}

	mux := http.NewServeMux()
	commsPath, commsH := commsv1connect.NewCommsServiceHandler(commsHandler)
	mux.Handle(commsPath, commsH)

	rpcServer := httptest.NewServer(mux)
	t.Cleanup(rpcServer.Close)

	// Create the real ConnectRPC client pointing at the test server.
	commsClient := commsv1connect.NewCommsServiceClient(rpcServer.Client(), rpcServer.URL)

	// Create the bridge + hub.
	var b *bridge.Bridge

	hub := ws.NewHub(func(client *ws.Client, data []byte) {
		b.HandleMessage(client, data)
	})
	b = bridge.NewBridge(hub, commsClient)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go hub.Run(ctx)

	// Create the WebSocket server with the hub.
	wsMux := http.NewServeMux()
	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	wsMux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade error: %v", err)

			return
		}

		client := ws.NewClient(hub, conn)
		hub.Register(client)

		go client.WritePump()
		go client.ReadPump()
	})

	wsServer := httptest.NewServer(wsMux)
	t.Cleanup(wsServer.Close)

	// Connect a WebSocket client.
	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http") + "/ws"

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WS dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// Send a TX toggle message through the WebSocket.
	txToggle := []byte{ws.OpcodeTXToggle, 3, 1} // channel 3, on
	if err := conn.WriteMessage(websocket.BinaryMessage, txToggle); err != nil {
		t.Fatalf("WS write: %v", err)
	}

	// Verify the RPC was called with the expected payload.
	select {
	case req := <-commsHandler.sendCalls:
		if req.Talkgroup != 3 {
			t.Errorf("talkgroup = %d, want 3", req.Talkgroup)
		}

		if !req.Enabled {
			t.Error("enabled = false, want true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SetSendTalkGroup RPC")
	}

	// Send an RX toggle message.
	rxToggle := []byte{ws.OpcodeRXToggle, 1, 0} // channel 1, off
	if err := conn.WriteMessage(websocket.BinaryMessage, rxToggle); err != nil {
		t.Fatalf("WS write: %v", err)
	}

	select {
	case req := <-commsHandler.receiveCalls:
		if req.Talkgroup != 1 {
			t.Errorf("talkgroup = %d, want 1", req.Talkgroup)
		}

		if req.Enabled {
			t.Error("enabled = true, want false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SetReceiveTalkGroup RPC")
	}

	// Send a PTT-down message.
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte{ws.OpcodePTTDown}); err != nil {
		t.Fatalf("WS write: %v", err)
	}

	select {
	case req := <-commsHandler.pttCalls:
		if req.Event != 1 {
			t.Errorf("PTT event = %d, want 1", req.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SendPTTEvent RPC")
	}
}

// streamingCommsHandler serves StreamAudioRx with a repeating frame tagged
// with a fixed talkgroup, so the bridge's audio RX loop has live frames to
// forward for the duration of the test.
type streamingCommsHandler struct {
	commsv1connect.UnimplementedCommsServiceHandler

	talkgroup int32
}

func (h *streamingCommsHandler) StreamAudioRx(
	ctx context.Context,
	_ *commsv1.StreamAudioRxRequest,
	stream *connect.ServerStream[commsv1.StreamAudioRxResponse],
) error {
	var seq uint32

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			seq++
			if err := stream.Send(&commsv1.StreamAudioRxResponse{
				OpusData:  []byte{0xAA, 0xBB},
				Sequence:  seq,
				Talkgroup: h.talkgroup,
			}); err != nil {
				return err
			}
		}
	}
}

// TestIntegration_AudioRXCarriesTalkgroupChannel pins the end-to-end talk
// group attribution path: a StreamAudioRx frame tagged talkgroup 2 must
// reach a WebSocket client subscribed to channel 2 with channel byte 2 —
// not the historic hardcoded channel 1 — so per-talkgroup RX toggles and
// UI attribution work.
func TestIntegration_AudioRXCarriesTalkgroupChannel(t *testing.T) {
	commsHandler := &streamingCommsHandler{talkgroup: 2}

	mux := http.NewServeMux()
	commsPath, commsH := commsv1connect.NewCommsServiceHandler(commsHandler)
	mux.Handle(commsPath, commsH)

	rpcServer := httptest.NewServer(mux)
	t.Cleanup(rpcServer.Close)

	commsClient := commsv1connect.NewCommsServiceClient(rpcServer.Client(), rpcServer.URL)

	var b *bridge.Bridge

	hub := ws.NewHub(func(client *ws.Client, data []byte) {
		b.HandleMessage(client, data)
	})
	b = bridge.NewBridge(hub, commsClient)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go hub.Run(ctx)

	wsMux := http.NewServeMux()
	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	wsMux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade error: %v", err)

			return
		}

		client := ws.NewClient(hub, conn)
		hub.Register(client)

		go client.WritePump()
		go client.ReadPump()
	})

	wsServer := httptest.NewServer(wsMux)
	t.Cleanup(wsServer.Close)

	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http") + "/ws"

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WS dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// Subscribe to RX on channel 2 only — the frame must arrive because
	// the daemon tagged it talkgroup 2, proving the channel byte is the
	// talkgroup and the hub filter honors it.
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte{ws.OpcodeRXToggle, 2, 1}); err != nil {
		t.Fatalf("WS write: %v", err)
	}

	// Start the audio RX loop after the subscription so no frame is
	// dropped for lack of subscribers while the toggle is in flight.
	b.StartAudioRXLoop(ctx)

	deadline := time.Now().Add(5 * time.Second)
	if err := conn.SetReadDeadline(deadline); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	for time.Now().Before(deadline) {
		_, data, readErr := conn.ReadMessage()
		if readErr != nil {
			t.Fatalf("WS read: %v", readErr)
		}

		if len(data) < ws.AudioRXHeaderSize || data[0] != ws.OpcodeAudioRX {
			continue
		}

		if got := data[1]; got != 2 {
			t.Fatalf("WS audio frame channel byte: got %d, want 2 (talkgroup lost in transit)", got)
		}

		return // frame arrived with the right channel byte
	}

	t.Fatal("timed out waiting for an audio RX frame on the WS client")
}
