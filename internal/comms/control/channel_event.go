package control

// ChannelEventSource reports relative channel steps separately from PTT.
// Values are +1 (next) or -1 (previous). Events must never initiate transmit.
type ChannelEventSource interface {
	ChannelEvents() <-chan int
}

func queueChannelStep(ch chan int, step int) {
	// A bounded queue cannot block the PTT/control reader if a consumer stops.
	select {
	case ch <- step:
	default:
	}
}

// SpeakerVolumeEventSource reports accessory-originated HFP gain (0..15).
// Host mute commands and responses must not enter this channel.
type SpeakerVolumeEventSource interface{ SpeakerVolumes() <-chan int }
