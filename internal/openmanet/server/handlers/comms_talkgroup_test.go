package handlers_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	commsv1 "github.com/openmanet/openmanetd/internal/api/openmanet/comms/v1"
	"github.com/openmanet/openmanetd/internal/comms"
	"github.com/openmanet/openmanetd/internal/comms/talkgroup"
	"github.com/openmanet/openmanetd/internal/openmanet/server/handlers"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestSelectTalkGroup_CommsDisabled(t *testing.T) {
	svc := &handlers.CommsService{
		Log:     zerolog.Nop(),
		Cfg:     setupCommsTestConfig(t, "comms:\n  enable: false\n"),
		Service: func() *comms.Service { return nil },
	}

	_, err := svc.SelectTalkGroup(context.Background(),
		&commsv1.SelectTalkGroupRequest{Talkgroup: 2})
	require.Error(t, err)

	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeFailedPrecondition, connectErr.Code())
}

func TestSelectTalkGroup_NotRunning(t *testing.T) {
	svc := &handlers.CommsService{
		Log:     zerolog.Nop(),
		Cfg:     setupCommsTestConfig(t, "comms:\n  enable: true\n"),
		Service: func() *comms.Service { return nil }, // comms enabled but not running
	}

	_, err := svc.SelectTalkGroup(context.Background(),
		&commsv1.SelectTalkGroupRequest{Talkgroup: 2})
	require.Error(t, err)

	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeFailedPrecondition, connectErr.Code())
}

func TestTalkGroupEventToProto(t *testing.T) {
	at := time.Now()

	got := handlers.TalkGroupEventToProto(talkgroup.Event{
		Kind: talkgroup.KindSelected, Channel: 3, Prev: 1,
		Send: true, Receive: true, Source: talkgroup.SourceGPIO, At: at,
	})

	assert.Equal(t, int32(3), got.Talkgroup)
	assert.Equal(t, int32(1), got.PrevTalkgroup)
	assert.True(t, got.SendEnabled)
	assert.True(t, got.ReceiveEnabled)
	assert.Equal(t, at.Unix(), got.At.AsTime().Unix())
}

// TestTalkGroupEventToProto_KindMapping pins every talkgroup.Kind value
// against its proto constant. TalkGroupEventToProto uses a direct numeric
// cast (commsv1.TalkGroupEventKind(ev.Kind)), so a future proto renumber
// of any value here would silently corrupt events with nothing else to
// catch it.
func TestTalkGroupEventToProto_KindMapping(t *testing.T) {
	cases := map[talkgroup.Kind]commsv1.TalkGroupEventKind{
		talkgroup.KindSelected:  commsv1.TalkGroupEventKind_TALK_GROUP_EVENT_KIND_SELECTED,
		talkgroup.KindDirection: commsv1.TalkGroupEventKind_TALK_GROUP_EVENT_KIND_DIRECTION,
	}

	for in, want := range cases {
		got := handlers.TalkGroupEventToProto(talkgroup.Event{Kind: in})
		assert.Equalf(t, want, got.Kind, "Kind %d", in)
	}
}

// TestTalkGroupEventToProto_SourceMapping pins every talkgroup.Source value
// against its proto constant, for the same reason as the Kind mapping test.
func TestTalkGroupEventToProto_SourceMapping(t *testing.T) {
	cases := map[talkgroup.Source]commsv1.TalkGroupEventSource{
		talkgroup.SourceRPC:  commsv1.TalkGroupEventSource_TALK_GROUP_EVENT_SOURCE_RPC,
		talkgroup.SourceGPIO: commsv1.TalkGroupEventSource_TALK_GROUP_EVENT_SOURCE_GPIO,
		talkgroup.SourceInit: commsv1.TalkGroupEventSource_TALK_GROUP_EVENT_SOURCE_INIT,
	}

	for in, want := range cases {
		got := handlers.TalkGroupEventToProto(talkgroup.Event{Source: in})
		assert.Equalf(t, want, got.Source, "Source %d", in)
	}
}

func TestStreamTalkGroupEvents_NotRunning(t *testing.T) {
	svc := &handlers.CommsService{
		Log:     zerolog.Nop(),
		Cfg:     setupCommsTestConfig(t, "comms:\n  enable: true\n"),
		Service: func() *comms.Service { return nil },
	}

	err := svc.StreamTalkGroupEvents(context.Background(), &emptypb.Empty{}, nil)
	require.Error(t, err)

	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeFailedPrecondition, connectErr.Code())
}

func TestTalkGroupEventListener_DeliversUntilFullThenDropsNewest(t *testing.T) {
	reg := talkgroup.NewRegistry(zerolog.Nop())
	ch := make(chan *commsv1.TalkGroupEvent, 16)

	fn := handlers.TalkGroupEventListener(ch, reg)

	// 17 events into a 16-slot buffer: the 17th is dropped and counted.
	for i := 1; i <= 17; i++ {
		fn(talkgroup.Event{
			Kind: talkgroup.KindSelected, Channel: i,
			Send: true, Receive: true,
			Source: talkgroup.SourceRPC, At: time.Now(),
		})
	}

	assert.Equal(t, uint64(1), reg.Dropped(), "one overflow event counted")
	require.Len(t, ch, 16, "buffer holds the first 16 events")

	for want := 1; want <= 16; want++ {
		got := <-ch
		assert.Equal(t, int32(want), got.Talkgroup, "delivery order preserved")
	}
}

func TestTalkGroupEventListener_RecoversAfterDrain(t *testing.T) {
	reg := talkgroup.NewRegistry(zerolog.Nop())
	ch := make(chan *commsv1.TalkGroupEvent, 1)

	fn := handlers.TalkGroupEventListener(ch, reg)

	fn(talkgroup.Event{Kind: talkgroup.KindSelected, Channel: 1})
	fn(talkgroup.Event{Kind: talkgroup.KindSelected, Channel: 2}) // dropped
	assert.Equal(t, uint64(1), reg.Dropped())

	<-ch // consumer catches up

	fn(talkgroup.Event{Kind: talkgroup.KindSelected, Channel: 3})
	assert.Equal(t, uint64(1), reg.Dropped(), "no drop once the buffer has room")
	assert.Equal(t, int32(3), (<-ch).Talkgroup)
}
