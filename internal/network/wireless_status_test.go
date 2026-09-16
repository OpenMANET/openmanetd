package network

import (
	"context"
	"errors"
	"testing"

	"github.com/openmanet/openmanetd/internal/iwinfo"
	"github.com/stretchr/testify/assert"
)

type wsUbusExecutor struct {
	output []byte
	err    error
}

func (m *wsUbusExecutor) Execute(_ context.Context, _ ...string) ([]byte, error) {
	return m.output, m.err
}

func TestGetWirelessStatus_MultipleRadios(t *testing.T) {
	fixture := `{
  "radio2": {
    "up": true,
    "disabled": false,
    "interfaces": [
      {
        "section": "default_radio2",
        "ifname": "phy0-ap0",
        "config": {
          "mode": "ap"
        }
      }
    ]
  },
  "radio3": {
    "up": true,
    "disabled": false,
    "interfaces": [
      {
        "section": "default_radio3",
        "ifname": "phy1-mesh0",
        "config": {
          "mode": "mesh"
        }
      }
    ]
  }
}`
	provider := NewUbusWirelessStatusProvider(&wsUbusExecutor{output: []byte(fixture)})

	result, err := provider.GetWirelessStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("expected 2 radios, got %d", len(result))
	}

	r2 := result["radio2"]
	if r2 == nil {
		t.Fatal("expected radio2 in result")
	}

	if !r2.Up {
		t.Error("expected radio2 to be up")
	}

	if len(r2.Interfaces) != 1 {
		t.Fatalf("expected 1 interface for radio2, got %d", len(r2.Interfaces))
	}

	if r2.Interfaces[0].Ifname != "phy0-ap0" {
		t.Errorf("expected ifname phy0-ap0, got %s", r2.Interfaces[0].Ifname)
	}

	if r2.Interfaces[0].Section != "default_radio2" {
		t.Errorf("expected section default_radio2, got %s", r2.Interfaces[0].Section)
	}

	if r2.Interfaces[0].Config.Mode != "ap" {
		t.Errorf("expected mode ap, got %s", r2.Interfaces[0].Config.Mode)
	}

	r3 := result["radio3"]
	if r3 == nil {
		t.Fatal("expected radio3 in result")
	}

	if r3.Interfaces[0].Ifname != "phy1-mesh0" {
		t.Errorf("expected ifname phy1-mesh0, got %s", r3.Interfaces[0].Ifname)
	}

	if r3.Interfaces[0].Config.Mode != "mesh" {
		t.Errorf("expected mode mesh, got %s", r3.Interfaces[0].Config.Mode)
	}
}

func TestResolveWirelessRadioHardwareName_interfaces(t *testing.T) {
	t.Parallel()

	status := map[string]*WirelessRadioStatus{
		"radio2": {Interfaces: []WirelessRadioInterface{
			{Ifname: "missing"},
			{Ifname: "phy1-mesh0"},
		}},
	}
	info := map[string]*iwinfo.InterfaceInfo{
		"phy1-mesh0": {Hardware: iwinfo.HardwareInfo{Name: "MediaTek MT7915AN"}},
	}

	assert.Equal(t, "MediaTek MT7915AN", ResolveWirelessRadioHardwareName("radio2", status, info))
	assert.Empty(t, ResolveWirelessRadioHardwareName("missing", status, info))
	assert.Empty(t, ResolveWirelessRadioHardwareName("radio2", status, nil))
}

func TestGetWirelessStatus_DisabledRadio(t *testing.T) {
	fixture := `{
  "radio1": {
    "up": false,
    "disabled": true,
    "interfaces": []
  }
}`
	provider := NewUbusWirelessStatusProvider(&wsUbusExecutor{output: []byte(fixture)})

	result, err := provider.GetWirelessStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	r1 := result["radio1"]
	if r1 == nil {
		t.Fatal("expected radio1 in result")
	}

	if r1.Up {
		t.Error("expected radio1 to be down")
	}

	if !r1.Disabled {
		t.Error("expected radio1 to be disabled")
	}

	if len(r1.Interfaces) != 0 {
		t.Errorf("expected 0 interfaces, got %d", len(r1.Interfaces))
	}
}

func TestGetWirelessStatus_EmptyResponse(t *testing.T) {
	provider := NewUbusWirelessStatusProvider(&wsUbusExecutor{output: []byte(`{}`)})

	result, err := provider.GetWirelessStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("expected empty result, got %d entries", len(result))
	}
}

func TestGetWirelessStatus_UbusError(t *testing.T) {
	provider := NewUbusWirelessStatusProvider(&wsUbusExecutor{err: errors.New("ubus timeout")})

	_, err := provider.GetWirelessStatus(context.Background())
	if err == nil {
		t.Fatal("expected error when ubus fails")
	}
}

func TestGetWirelessStatus_MalformedJSON(t *testing.T) {
	provider := NewUbusWirelessStatusProvider(&wsUbusExecutor{output: []byte(`{invalid}`)})

	_, err := provider.GetWirelessStatus(context.Background())
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestGetWirelessStatus_MultipleInterfacesPerRadio(t *testing.T) {
	fixture := `{
  "radio0": {
    "up": true,
    "disabled": false,
    "interfaces": [
      {
        "section": "default_radio0",
        "ifname": "phy0-ap0",
        "config": {
          "mode": "ap"
        }
      },
      {
        "section": "mesh_radio0",
        "ifname": "phy0-mesh0",
        "config": {
          "mode": "mesh"
        }
      }
    ]
  }
}`
	provider := NewUbusWirelessStatusProvider(&wsUbusExecutor{output: []byte(fixture)})

	result, err := provider.GetWirelessStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	r0 := result["radio0"]
	if r0 == nil {
		t.Fatal("expected radio0 in result")
	}

	if len(r0.Interfaces) != 2 {
		t.Fatalf("expected 2 interfaces for radio0, got %d", len(r0.Interfaces))
	}

	if r0.Interfaces[0].Section != "default_radio0" {
		t.Errorf("first interface section: got %s, want default_radio0", r0.Interfaces[0].Section)
	}

	if r0.Interfaces[1].Ifname != "phy0-mesh0" {
		t.Errorf("second interface ifname: got %s, want phy0-mesh0", r0.Interfaces[1].Ifname)
	}
}

func TestSupportsSecondaryMeshLink(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"MediaTek MT7915AN", true},
		{"MediaTek MT7916AN", true},
		{"MT7916", true},
		{"MediaTek MT7921", false},
		{"Broadcom BCM43430", false},
		{"Morse Micro MM6108", false},
		{"", false},
	}

	for _, tc := range cases {
		assert.Equal(t, tc.want, SupportsSecondaryMeshLink(tc.name), "hardware %q", tc.name)
	}
}
