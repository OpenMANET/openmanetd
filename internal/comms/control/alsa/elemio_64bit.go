//go:build linux && (amd64 || arm64)

package alsa

// ctlLong mirrors the C `long` used by the kernel's element value union on
// 64-bit targets.
type ctlLong = int64

const (
	// elemValueUnionSize is sizeof the value union: long value[128].
	elemValueUnionSize = 1024
	// elemValueSize is sizeof(struct snd_ctl_elem_value).
	elemValueSize = 1224

	// Generic asm ioctl encoding: 14 size bits, direction field at bit 30
	// with _IOC_WRITE=1 and _IOC_READ=2.
	iocDirRW    = 3
	iocDirShift = 30
)
