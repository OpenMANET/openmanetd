// White-box tests for the direct element value I/O layer. These live in
// package alsa (not alsa_test) because they verify unexported kernel-ABI
// plumbing — struct layout, ioctl request encoding, and the read-modify-
// write cycle — through the injectable doIoctl seam.
package alsa

import (
	"errors"
	"runtime"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeElemIoctl returns an elemIO whose ioctls serve reads from initial and
// capture the last written element value.
func fakeElemIoctl(t *testing.T, wantNumid uint32, initial []ctlLong) (*elemIO, func() *elemValue) {
	t.Helper()

	var wrote *elemValue

	e := &elemIO{doIoctl: func(_ uintptr, req uintptr, val *elemValue) error {
		require.EqualValues(t, wantNumid, val.id.numid, "ioctl must address the element by numid")

		switch req {
		case sndrvCtlIoctlElemRead:
			copy(val.longs(uint32(len(initial))), initial)
		case sndrvCtlIoctlElemWrite:
			cp := *val
			wrote = &cp
		default:
			t.Fatalf("unexpected ioctl request 0x%x", req)
		}

		return nil
	}}

	return e, func() *elemValue { return wrote }
}

func TestElemIO_SetValue_WritesSecondChannel(t *testing.T) {
	// Regression for the gen2brain/alsa int32 overlay bug: on a stereo
	// control, writing index 1 must land on the kernel's long[1], leaving
	// long[0] untouched — not on the high half of long[0].
	e, wrote := fakeElemIoctl(t, 4, []ctlLong{37, 37})

	require.NoError(t, e.setValue(4, 2, 1, 7))

	w := wrote()
	require.NotNil(t, w, "an ELEM_WRITE must be issued")
	assert.Equal(t, []ctlLong{37, 7}, w.longs(2))

	// Byte-level layout check: channel 1 starts exactly one ctlLong into
	// the value union — this is the offset the upstream library gets wrong.
	step := unsafe.Sizeof(ctlLong(0))

	var ch1 int64
	for i := uintptr(0); i < step; i++ {
		ch1 |= int64(w.value[step+i]) << (8 * i)
	}

	assert.EqualValues(t, 7, ch1)
}

func TestElemIO_SetValue_PreservesOtherChannels(t *testing.T) {
	e, wrote := fakeElemIoctl(t, 9, []ctlLong{3, 12})

	require.NoError(t, e.setValue(9, 2, 0, 5))

	w := wrote()
	require.NotNil(t, w)
	assert.Equal(t, []ctlLong{5, 12}, w.longs(2))
}

func TestElemIO_Value_ReadsIndexedChannel(t *testing.T) {
	e, _ := fakeElemIoctl(t, 4, []ctlLong{7, 37})

	v0, err := e.value(4, 2, 0)
	require.NoError(t, err)
	assert.Equal(t, 7, v0)

	v1, err := e.value(4, 2, 1)
	require.NoError(t, err)
	assert.Equal(t, 37, v1)
}

func TestElemIO_BoundsChecks(t *testing.T) {
	e, _ := fakeElemIoctl(t, 1, []ctlLong{0})

	_, err := e.value(1, 1, 1)
	assert.Error(t, err, "index beyond count must error")

	err = e.setValue(1, 1, 1, 0)
	assert.Error(t, err, "index beyond count must error")

	_, err = e.value(1, 0, 0)
	assert.Error(t, err, "zero-count element must error")

	err = e.setValue(1, 200, 0, 0)
	assert.Error(t, err, "count beyond the value union capacity must error")
}

func TestElemIO_IoctlErrorsPropagate(t *testing.T) {
	boom := errors.New("boom")

	e := &elemIO{doIoctl: func(_, _ uintptr, _ *elemValue) error { return boom }}

	_, err := e.value(1, 1, 0)
	require.ErrorIs(t, err, boom)

	err = e.setValue(1, 1, 0, 0)
	require.ErrorIs(t, err, boom)

	// Write-side failure after a successful read must also surface.
	e = &elemIO{doIoctl: func(_, req uintptr, _ *elemValue) error {
		if req == sndrvCtlIoctlElemWrite {
			return boom
		}

		return nil
	}}
	err = e.setValue(1, 1, 0, 0)
	require.ErrorIs(t, err, boom)
}

func TestElemIO_RequestEncoding(t *testing.T) {
	// Known-good request numbers from the kernel UAPI on the generic
	// (non-MIPS) encoding; MIPS uses different direction bits and is
	// covered by the compile-time size assertions plus cross-compilation.
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("encoding literals are for 64-bit generic ABI, GOARCH=%s", runtime.GOARCH)
	}

	assert.EqualValues(t, 0xc4c85512, sndrvCtlIoctlElemRead)
	assert.EqualValues(t, 0xc4c85513, sndrvCtlIoctlElemWrite)
}

func TestElemValue_KernelLayout(t *testing.T) {
	// The value union must start at byte 72 (after the 64-byte id, the
	// 4-byte indirect bitfield, and 4 bytes of alignment padding) on every
	// supported target; total size is arch-dependent and pinned by the
	// compile-time assertions next to the struct definition.
	assert.EqualValues(t, 64, unsafe.Offsetof(elemValue{}.indirect))
	assert.EqualValues(t, 72, unsafe.Offsetof(elemValue{}.value))
	assert.EqualValues(t, elemValueSize, unsafe.Sizeof(elemValue{}))
}
