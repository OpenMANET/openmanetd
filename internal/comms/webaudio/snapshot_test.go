package webaudio_test

import (
	"testing"

	"github.com/openmanet/openmanetd/internal/comms/webaudio"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
)

func TestBridge_Snapshot_NilSafe(t *testing.T) {
	t.Parallel()

	var b *webaudio.Bridge

	var dst webaudio.BridgeSnapshot

	b.Snapshot(&dst)
	b.Snapshot(nil)
}

func TestBridge_Snapshot_ReadsCounters(t *testing.T) {
	t.Parallel()

	b := webaudio.NewBridge(zerolog.Nop(), nil)
	b.PushRxFrame(1, []byte{0x01})
	b.PushRxFrame(1, []byte{0x02})
	b.PushRxFrame(1, []byte{0x03})

	var dst webaudio.BridgeSnapshot

	b.Snapshot(&dst)

	assert.Equal(t, int64(3), dst.RxPushIn)
	assert.Equal(t, int64(0), dst.RxPushDrop)
}

func TestBridge_Snapshot_ConsumerFields(t *testing.T) {
	t.Parallel()

	b := webaudio.NewBridge(zerolog.Nop(), nil)
	b.AddConsumer()
	b.RxGatedNoConsumer.Add(7)

	var dst webaudio.BridgeSnapshot

	b.Snapshot(&dst)

	assert.Equal(t, int32(1), dst.Consumers)
	assert.Equal(t, int64(7), dst.RxGatedNoConsumer)

	b.RemoveConsumer()
	b.Snapshot(&dst)
	assert.Equal(t, int32(0), dst.Consumers)
}

func TestBridge_Snapshot_ZeroAlloc(t *testing.T) {
	b := webaudio.NewBridge(zerolog.Nop(), nil)
	b.PushRxFrame(1, []byte{0x01})

	var dst webaudio.BridgeSnapshot

	allocs := testing.AllocsPerRun(100, func() {
		b.Snapshot(&dst)
	})

	assert.Equal(t, 0.0, allocs, "Bridge.Snapshot must not allocate")
}
