// Package talkgroup defines the talk group change-event vocabulary and a
// listener registry. It is a dependency-free leaf (like audiopool and
// codec) so both the comms parent and the RPC handlers layer can import
// it without cycles.
package talkgroup

import "time"

// Source identifies which control surface produced a talk group change.
type Source uint8

const (
	// SourceRPC marks changes from SelectTalkGroup / SetSendTalkGroup /
	// SetReceiveTalkGroup RPCs (web UI and API clients).
	SourceRPC Source = iota + 1
	// SourceGPIO marks changes from the hardware selector.
	SourceGPIO
	// SourceInit marks boot-time seeding. The announcer excludes it.
	SourceInit
)

// String returns the log-friendly name of the source.
func (s Source) String() string {
	switch s {
	case SourceRPC:
		return "rpc"
	case SourceGPIO:
		return "gpio"
	case SourceInit:
		return "init"
	default:
		return "unknown"
	}
}

// Kind distinguishes exclusive selections from single-direction toggles.
type Kind uint8

const (
	// KindSelected: an exclusive selection changed the active group.
	KindSelected Kind = iota + 1
	// KindDirection: a single RX or TX toggle changed on one group.
	KindDirection
)

// Event is a single talk group state-change observation. Field order is
// alignment-optimal (fieldalignment): 48 bytes instead of 56.
type Event struct {
	At      time.Time
	Channel int // 1-based talk group the event is about
	Prev    int // previous active channel (KindSelected only, 0 if none)
	Kind    Kind
	Send    bool
	Receive bool
	Source  Source
}
