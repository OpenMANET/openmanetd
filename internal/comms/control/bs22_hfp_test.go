package control

import (
	"github.com/godbus/dbus/v5"
	"testing"
)

func TestBS22HFPPCMRequiresMatchingDeviceAndTransport(t *testing.T) {
	dev := dbus.ObjectPath("/org/bluez/hci0/dev_AA")
	props := map[string]dbus.Variant{"Device": dbus.MakeVariant(dev), "Transport": dbus.MakeVariant("HFP-AG")}
	objects := bluezManagedObjectMap{"/pcm": {"org.bluealsa.PCM1": props}}
	if !hasBS22HFPPCM(objects, dev) {
		t.Fatal("missing matching HFP PCM")
	}
	if hasBS22HFPPCM(objects, "/other") {
		t.Fatal("accepted another headset")
	}
	props["Transport"] = dbus.MakeVariant("A2DP-source")
	if hasBS22HFPPCM(objects, dev) {
		t.Fatal("accepted A2DP as duplex HFP")
	}
}

func TestHFPReadyRequiresBothRunningDirections(t *testing.T) {
	dev := dbus.ObjectPath("/org/bluez/hci0/dev_AA")
	source := map[string]dbus.Variant{"Device": dbus.MakeVariant(dev), "Transport": dbus.MakeVariant("HFP-AG"), "Mode": dbus.MakeVariant("source"), "Running": dbus.MakeVariant(true)}
	sink := map[string]dbus.Variant{"Device": dbus.MakeVariant(dev), "Transport": dbus.MakeVariant("HFP-AG"), "Mode": dbus.MakeVariant("sink"), "Running": dbus.MakeVariant(false)}
	objects := bluezManagedObjectMap{"/source": {"org.bluealsa.PCM1": source}, "/sink": {"org.bluealsa.PCM1": sink}}
	if hasRunningBS22HFPAudio(objects, dev) {
		t.Fatal("accepted idle speaker")
	}
	sink["Running"] = dbus.MakeVariant(true)
	if !hasRunningBS22HFPAudio(objects, dev) {
		t.Fatal("rejected running duplex audio")
	}
	source["Device"] = dbus.MakeVariant(dbus.ObjectPath("/other"))
	if hasRunningBS22HFPAudio(objects, dev) {
		t.Fatal("combined different accessories")
	}
}
