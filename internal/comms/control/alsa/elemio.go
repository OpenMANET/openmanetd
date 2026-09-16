package alsa

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// This file reads and writes mixer element values against
// /dev/snd/controlC<card> directly. It exists because gen2brain/alsa
// (through v0.6.0) overlays the kernel's `long value[128]` array as int32
// entries, which on 64-bit targets makes every channel past the first
// unreachable: index 1 lands on the high half of channel 0, so the second
// channel of a stereo control (e.g. the CM108B's "PCM Playback Volume")
// can never be written and the driver silently truncates the garbage.
// The library remains in use for enumeration and element metadata, where
// its layouts are correct. Only INTEGER and BOOLEAN elements route through
// this path — ENUMERATED elements use a u32 array in the kernel union and
// INTEGER64 a different member, so both keep the library accessors.

// ioctl request numbers for struct snd_ctl_elem_value transfers, built
// from the per-arch encoding constants (see elemio_64bit.go, elemio_mips.go).
const (
	sndrvCtlIoctlElemRead  uintptr = (iocDirRW << iocDirShift) | (elemValueSize << 16) | ('U' << 8) | 0x12
	sndrvCtlIoctlElemWrite uintptr = (iocDirRW << iocDirShift) | (elemValueSize << 16) | ('U' << 8) | 0x13
)

// elemID mirrors struct snd_ctl_elem_id (64 bytes on all targets).
type elemID struct {
	numid     uint32
	iface     int32
	device    uint32
	subdevice uint32
	name      [44]byte
	index     uint32
}

// elemValue mirrors struct snd_ctl_elem_value. The value union is kept as
// raw bytes; INTEGER/BOOLEAN payloads overlay it as ctlLong entries.
type elemValue struct {
	id       elemID
	indirect uint32 // unsigned int indirect:1 in the kernel header
	_        [4]byte
	value    [elemValueUnionSize]byte
	reserved [128]byte
}

// Compile-time kernel-ABI checks: each pair only compiles when the two
// sides are equal (a mismatch produces a negative untyped constant, which
// cannot convert to uintptr).
const (
	_ uintptr = unsafe.Sizeof(elemValue{}) - elemValueSize
	_ uintptr = elemValueSize - unsafe.Sizeof(elemValue{})
	_ uintptr = unsafe.Offsetof(elemValue{}.value) - 72
	_ uintptr = 72 - unsafe.Offsetof(elemValue{}.value)
	_ uintptr = unsafe.Sizeof(elemID{}) - 64
	_ uintptr = 64 - unsafe.Sizeof(elemID{})
)

// longs overlays the value union as count ctlLong entries. Callers must
// have validated count via checkChannelBounds.
func (v *elemValue) longs(count uint32) []ctlLong {
	return unsafe.Slice((*ctlLong)(unsafe.Pointer(&v.value[0])), count)
}

// elemIO issues element value ioctls against one card's control node.
// Methods are stateless per call; concurrent use is safe at the fd level
// but read-modify-write cycles are not atomic against other writers (the
// same holds for alsa-lib clients).
type elemIO struct {
	f *os.File
	// doIoctl is swappable in tests; nil uses the real ioctl syscall.
	doIoctl func(fd uintptr, req uintptr, val *elemValue) error
}

// openElemIO opens the control device node for the given card index.
func openElemIO(card uint) (*elemIO, error) {
	f, err := os.OpenFile(fmt.Sprintf("/dev/snd/controlC%d", card), os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open control device: %w", err)
	}

	return &elemIO{f: f}, nil
}

func (e *elemIO) Close() error {
	if e.f == nil {
		return nil
	}

	if err := e.f.Close(); err != nil {
		return fmt.Errorf("close control device: %w", err)
	}

	return nil
}

func (e *elemIO) ioctl(req uintptr, val *elemValue) error {
	var fd uintptr
	if e.f != nil {
		fd = e.f.Fd()
	}

	if e.doIoctl != nil {
		return e.doIoctl(fd, req, val)
	}

	_, _, errno := unix.Syscall(unix.SYS_IOCTL, fd, req, uintptr(unsafe.Pointer(val)))
	if errno != 0 {
		return fmt.Errorf("ioctl 0x%x: %w", req, errno)
	}

	return nil
}

// value reads channel index of the element identified by numid, which the
// kernel resolves directly when non-zero (no name lookup needed).
func (e *elemIO) value(numid, count uint32, index uint) (int, error) {
	if err := checkChannelBounds(index, count); err != nil {
		return 0, err
	}

	var val elemValue

	val.id.numid = numid

	if err := e.ioctl(sndrvCtlIoctlElemRead, &val); err != nil {
		return 0, fmt.Errorf("elem read numid=%d: %w", numid, err)
	}

	return int(val.longs(count)[index]), nil
}

// setValue writes channel index of the element identified by numid,
// preserving all other channels via a read-modify-write cycle.
func (e *elemIO) setValue(numid, count uint32, index uint, value int) error {
	if err := checkChannelBounds(index, count); err != nil {
		return err
	}

	var val elemValue

	val.id.numid = numid

	if err := e.ioctl(sndrvCtlIoctlElemRead, &val); err != nil {
		return fmt.Errorf("elem read numid=%d: %w", numid, err)
	}

	val.longs(count)[index] = ctlLong(value)

	if err := e.ioctl(sndrvCtlIoctlElemWrite, &val); err != nil {
		return fmt.Errorf("elem write numid=%d: %w", numid, err)
	}

	return nil
}

// checkChannelBounds validates a channel index against the element's
// channel count and the value union's ctlLong capacity.
func checkChannelBounds(index uint, count uint32) error {
	const maxChannels = elemValueUnionSize / unsafe.Sizeof(ctlLong(0))

	if count == 0 || uintptr(count) > maxChannels {
		return fmt.Errorf("element channel count %d outside [1, %d]", count, maxChannels)
	}

	if index >= uint(count) {
		return fmt.Errorf("channel %d out of range (count %d)", index, count)
	}

	return nil
}
