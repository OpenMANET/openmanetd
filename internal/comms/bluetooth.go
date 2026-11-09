package comms

import (
	"errors"
	"github.com/openmanet/openmanetd/internal/config"
	"net"
	"strings"
)

// BluetoothSession separates permission to establish audio/button links from
// permission to transmit mesh voice. Implementations must be concurrency safe.
type BluetoothSession interface {
	ConnectionAllowed() bool
	CanTransmit() bool
	ObservePTT(pressed bool)
	ObserveAudioReady(bool)
	ObserveButtonsReady(bool)
	ObserveHFPReady(bool)
}

var ErrBluetoothDisabled = errors.New("start Bluetooth setup before enabling Bluetooth comms")

func (cfg *CommsConfig) usesBluetooth() bool {
	source := normalizeControlSource(cfg.ControlSource)
	return source == controlSourceBS22 || source == controlSourceBlueALSAXEvent || cfg.EnableBluetoothPtt
}

func (cfg *CommsConfig) bluetoothConnectionAllowed() bool {
	return !cfg.usesBluetooth() || (cfg.BluetoothSession != nil && cfg.BluetoothSession.ConnectionAllowed())
}

func (cfg *CommsConfig) transmissionAllowed() bool {
	return !cfg.usesBluetooth() || (cfg.BluetoothSession != nil && cfg.BluetoothSession.CanTransmit())
}

// stopTransmissionQuietly closes the capture gate without producing a tone on
// removed/disabled hardware. Run owns this call, including its deferred exit.
func (cfg *CommsConfig) stopTransmissionQuietly(rt *CommsRuntime) {
	if !rt.Broadcasting.Swap(false) {
		return
	}
	if bs := rt.Broadcast(); bs != nil {
		bs.SetTxEnabled(false)
	}
}

// selectedBluetoothAudioSpec binds ALSA to the same MAC as the selected BlueZ
// object, avoiding the generic alias which may resolve a different headset.
func (cfg *CommsConfig) selectedBluetoothAudioSpec() (string, error) {
	_, suffix, ok := strings.Cut(cfg.BluetoothDevicePath, "/dev_")
	if !ok {
		return "", errors.New("Bluetooth accessory is not selected")
	}
	mac, err := net.ParseMAC(strings.ReplaceAll(suffix, "_", ":"))
	if err != nil || len(mac) != 6 {
		return "", errors.New("invalid Bluetooth accessory address")
	}
	return "bluealsa:DEV=" + strings.ToUpper(mac.String()) + ",PROFILE=sco", nil
}

// stepBluetoothChannel runs on the same loop as PTT, so a channel press
// cannot race a local PTT transition or move an ongoing transmission.
func (cfg *CommsConfig) stepBluetoothChannel(rt *CommsRuntime, step int) error {
	if !cfg.usesBluetooth() || !cfg.transmissionAllowed() || rt.Broadcasting.Load() {
		return nil
	}
	if step != 1 && step != -1 {
		return nil
	}
	if len(rt.Ports) == 0 {
		return nil
	}
	current := int(rt.ActiveChannel.Load())
	index := -1
	for i, pc := range rt.Ports {
		channel, err := config.TalkGroupChannel(pc.cfg.Port)
		if err != nil {
			return err
		}
		if channel == current {
			index = i
			break
		}
	}
	if index < 0 {
		if step > 0 {
			index = len(rt.Ports) - 1
		} else {
			index = 0
		}
	}
	next := (index + step + len(rt.Ports)) % len(rt.Ports)
	channel, err := config.TalkGroupChannel(rt.Ports[next].cfg.Port)
	if err != nil {
		return err
	}
	svc := Service{Cfg: cfg, Rt: rt}
	// Source 0 is the API's unspecified source; do not mislabel as RPC/GPIO.
	return svc.SelectTalkGroup(channel, 0)
}
