//go:build linux && (mips || mipsle)

package alsa

// ctlLong mirrors the C `long` used by the kernel's element value union on
// 32-bit MIPS targets.
type ctlLong = int32

const (
	// elemValueUnionSize is sizeof the value union. The union's largest
	// members (long long value[64], unsigned char data[512]) are 512 bytes;
	// long value[128] is also 512 with 4-byte longs.
	elemValueUnionSize = 512
	// elemValueSize is sizeof(struct snd_ctl_elem_value): the union is
	// 8-byte aligned on the o32 ABI (long long members), so it still starts
	// at offset 72 as on 64-bit targets.
	elemValueSize = 712

	// MIPS ioctl encoding: 13 size bits, direction field at bit 29 with
	// _IOC_READ=2 and _IOC_WRITE=4.
	iocDirRW    = 6
	iocDirShift = 29
)
