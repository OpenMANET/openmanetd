package handlers_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/digineo/go-uci/v2"
	wificonfigv1 "github.com/openmanet/openmanetd/internal/api/openmanet/wifi_config/v1"
	"github.com/openmanet/openmanetd/internal/openmanet/server/handlers"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRadioSettingsPreserveExternalEdits(t *testing.T) {
	for _, batch := range []bool{false, true} {
		name := "single"
		if batch {
			name = "batch"
		}

		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "wireless"), []byte(`config wifi-device 'radio0'
 option type 'mac80211'
 option channel '36'
config wifi-iface 'ap'
 option device 'radio0'
 option mode 'ap'
 option network 'ahwlan'
 option ssid 'ap'
config wifi-device 'radio1'
 option type 'morse'
config wifi-iface 'halow'
 option device 'radio1'
 option mode 'mesh'
 option network 'ahwlan'
 option mesh_fwding '0'
`), 0600))
			cached := &diskWirelessReader{Tree: uci.NewTree(dir)}
			require.NoError(t, cached.LoadConfig("wireless", false))

			external := uci.NewTree(dir)
			require.NoError(t, external.SetType("wireless", "halow", "network", uci.TypeOption, "batmesh0"))
			require.NoError(t, external.SetType("wireless", "halow", "vendor_option", uci.TypeOption, "preserve-me"))
			require.NoError(t, external.Commit())

			svc := &handlers.WifiConfigService{Log: zerolog.Nop(), ConfigReader: cached, ReloadServices: func(context.Context) error { return nil }}

			settings := &wificonfigv1.RadioSettings{Ssid: "changed-ap", Channel: "6", Mode: wificonfigv1.WifiMode_WIFI_MODE_AP}
			if batch {
				require.NoError(t, svc.ApplyRadioSettingsBatch(context.Background(), []handlers.RadioSettingsUpdate{
					{RadioName: "radio0", Settings: settings},
					{RadioName: "radio1", Settings: &wificonfigv1.RadioSettings{Channel: "26"}},
				}))
			} else {
				resp, err := svc.UpdateRadioSettings(context.Background(), &wificonfigv1.UpdateRadioSettingsRequest{RadioName: "radio0", Settings: settings})
				require.NoError(t, err)
				require.True(t, resp.GetSuccess(), resp.GetMessage())
			}

			disk := uci.NewTree(dir)
			for option, want := range map[string]string{"network": "batmesh0", "vendor_option": "preserve-me", "mesh_fwding": "0"} {
				got, ok := disk.Get("wireless", "halow", option)
				require.True(t, ok)
				assert.Equal(t, []string{want}, got)
			}

			channel, _ := disk.Get("wireless", "radio0", "channel")
			assert.Equal(t, []string{"6"}, channel)
		})
	}
}
