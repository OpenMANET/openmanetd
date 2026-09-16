package handlers_test

import (
	"fmt"
	"strings"
	"testing"

	"buf.build/go/protovalidate"
	commsv1 "github.com/openmanet/openmanetd/internal/api/openmanet/comms/v1"
	meshjoinv1 "github.com/openmanet/openmanetd/internal/api/openmanet/mesh_join/v1"
	serviceproto "github.com/openmanet/openmanetd/internal/api/openmanet/service/v1"
	setupv1 "github.com/openmanet/openmanetd/internal/api/openmanet/setup/v1"
	wificonfigv1 "github.com/openmanet/openmanetd/internal/api/openmanet/wifi_config/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// newValidator creates a protovalidate.Validator and fails the test on error.
func newValidator(t *testing.T) protovalidate.Validator {
	t.Helper()

	v, err := protovalidate.New()
	require.NoError(t, err, "protovalidate.New()")

	return v
}

// ── GetNodeRequest ────────────────────────────────────────────────────────────

func TestValidation_GetNodeRequest_EmptyHostname(t *testing.T) {
	v := newValidator(t)

	err := v.Validate(&serviceproto.GetNodeRequest{Hostname: ""})
	assert.Error(t, err, "empty hostname must fail validation")
}

func TestValidation_GetNodeRequest_ValidHostname(t *testing.T) {
	v := newValidator(t)

	err := v.Validate(&serviceproto.GetNodeRequest{Hostname: "node-1"})
	assert.NoError(t, err)
}

// ── GetWirelessInterfaceRequest ───────────────────────────────────────────────

func TestValidation_GetWirelessInterfaceRequest_EmptyName(t *testing.T) {
	v := newValidator(t)

	err := v.Validate(&serviceproto.GetWirelessInterfaceRequest{Name: ""})
	assert.Error(t, err, "empty name must fail validation")
}

func TestValidation_GetWirelessInterfaceRequest_ValidName(t *testing.T) {
	v := newValidator(t)

	err := v.Validate(&serviceproto.GetWirelessInterfaceRequest{Name: "wlh0"})
	assert.NoError(t, err)
}

// ── SetSendTalkGroupRequest ───────────────────────────────────────────────────

func TestValidation_SetSendTalkGroupRequest_OutOfRange(t *testing.T) {
	v := newValidator(t)

	for _, tg := range []int32{0, -1, 33, 100} {
		tg := tg
		t.Run(fmt.Sprintf("talkgroup_%d", tg), func(t *testing.T) {
			err := v.Validate(&commsv1.SetSendTalkGroupRequest{Talkgroup: tg})
			assert.Error(t, err, "talkgroup %d is out of range [1,32] and must fail validation", tg)
		})
	}
}

func TestValidation_SetSendTalkGroupRequest_ValidTalkgroups(t *testing.T) {
	v := newValidator(t)

	for _, tg := range []int32{1, 16, 32} {
		tg := tg
		t.Run(fmt.Sprintf("talkgroup_%d", tg), func(t *testing.T) {
			err := v.Validate(&commsv1.SetSendTalkGroupRequest{Talkgroup: tg})
			assert.NoError(t, err, "talkgroup %d must pass validation", tg)
		})
	}
}

// ── SetReceiveTalkGroupRequest ────────────────────────────────────────────────

func TestValidation_SetReceiveTalkGroupRequest_OutOfRange(t *testing.T) {
	v := newValidator(t)

	for _, tg := range []int32{0, -1, 33, 100} {
		tg := tg
		t.Run(fmt.Sprintf("talkgroup_%d", tg), func(t *testing.T) {
			err := v.Validate(&commsv1.SetReceiveTalkGroupRequest{Talkgroup: tg})
			assert.Error(t, err, "talkgroup %d is out of range [1,32] and must fail validation", tg)
		})
	}
}

func TestValidation_SetReceiveTalkGroupRequest_ValidTalkgroups(t *testing.T) {
	v := newValidator(t)

	for _, tg := range []int32{1, 16, 32} {
		tg := tg
		t.Run(fmt.Sprintf("talkgroup_%d", tg), func(t *testing.T) {
			err := v.Validate(&commsv1.SetReceiveTalkGroupRequest{Talkgroup: tg})
			assert.NoError(t, err, "talkgroup %d must pass validation", tg)
		})
	}
}

// ── SelectTalkGroupRequest ────────────────────────────────────────────────────

func TestValidation_SelectTalkGroupRequest_OutOfRange(t *testing.T) {
	v := newValidator(t)

	for _, tg := range []int32{0, -1, 33, 100} {
		tg := tg
		t.Run(fmt.Sprintf("talkgroup_%d", tg), func(t *testing.T) {
			err := v.Validate(&commsv1.SelectTalkGroupRequest{Talkgroup: tg})
			assert.Error(t, err, "talkgroup %d is out of range [1,32] and must fail validation", tg)
		})
	}
}

func TestValidation_SelectTalkGroupRequest_ValidTalkgroups(t *testing.T) {
	v := newValidator(t)

	for _, tg := range []int32{1, 16, 32} {
		tg := tg
		t.Run(fmt.Sprintf("talkgroup_%d", tg), func(t *testing.T) {
			err := v.Validate(&commsv1.SelectTalkGroupRequest{Talkgroup: tg})
			assert.NoError(t, err, "talkgroup %d must pass validation", tg)
		})
	}
}

// ── UpdateAudioMixerRequest ───────────────────────────────────────────────────

func TestUpdateAudioMixerRequest_Validation(t *testing.T) {
	validator := newValidator(t)

	mk := func(v int32) *commsv1.UpdateAudioMixerRequest {
		return &commsv1.UpdateAudioMixerRequest{SpeakerVolume: &v}
	}

	tests := []struct {
		name    string
		msg     *commsv1.UpdateAudioMixerRequest
		wantErr bool
	}{
		{name: "empty request valid", msg: &commsv1.UpdateAudioMixerRequest{}},
		{name: "zero valid", msg: mk(0)},
		{name: "hundred valid", msg: mk(100)},
		{name: "negative invalid", msg: mk(-1), wantErr: true},
		{name: "over hundred invalid", msg: mk(101), wantErr: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := validator.Validate(tc.msg)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// ── MeshRadioConfig ───────────────────────────────────────────────────────────

func saeMeshRadioConfig() *setupv1.MeshRadioConfig {
	return &setupv1.MeshRadioConfig{
		RadioName:    "radio1",
		MeshId:       "openmanet-mesh",
		Passphrase:   "longpasscode",
		Encryption:   wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE,
		BandwidthMhz: 2,
		Channel:      42,
	}
}

func TestValidation_MeshRadioConfig_AcceptsSAE(t *testing.T) {
	v := newValidator(t)

	assert.NoError(t, v.Validate(saeMeshRadioConfig()))
}

func TestValidation_MeshRadioConfig_RejectsEverythingButSAE(t *testing.T) {
	v := newValidator(t)

	for _, enc := range []wificonfigv1.WifiEncryption{
		wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_NONE,
		wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_OWE,
		wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK2,
		wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE_MIXED,
	} {
		cfg := saeMeshRadioConfig()
		cfg.Encryption = enc

		assert.Error(t, v.Validate(cfg), "mesh links are SAE only; %s must be rejected", enc)
	}
}

// ── MeshBackhaulProfile ───────────────────────────────────────────────────────

func TestValidation_MeshBackhaulProfile_Boundaries(t *testing.T) {
	v := newValidator(t)

	cases := []struct {
		name   string
		meshID string
		pass   string
		ok     bool
	}{
		{"minimum", "a", "12345678", true},
		{"maximum", strings.Repeat("m", 32), strings.Repeat("p", 63), true},
		{"empty mesh id", "", "12345678", false},
		{"mesh id too long", strings.Repeat("m", 33), "12345678", false},
		{"passphrase too short", "backhaul", "1234567", false},
		{"passphrase too long", "backhaul", strings.Repeat("p", 64), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := v.Validate(&setupv1.MeshBackhaulProfile{MeshId: tc.meshID, Passphrase: tc.pass})
			if tc.ok {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// ── MeshCredentials / MeshJoinPayload ────────────────────────────────────────

func validMeshCredentials() *meshjoinv1.MeshCredentials {
	return &meshjoinv1.MeshCredentials{
		MeshId:       "field-mesh",
		Passphrase:   "correct-horse",
		Encryption:   wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE,
		BandwidthMhz: 8,
		Channel:      44,
		CountryCode:  "US",
	}
}

func TestValidation_MeshCredentials(t *testing.T) {
	v := newValidator(t)

	tests := []struct {
		name    string
		mutate  func(c *meshjoinv1.MeshCredentials)
		wantErr bool
	}{
		{name: "valid", mutate: func(*meshjoinv1.MeshCredentials) {}},
		{name: "empty mesh id", mutate: func(c *meshjoinv1.MeshCredentials) { c.MeshId = "" }, wantErr: true},
		{name: "33 char mesh id", mutate: func(c *meshjoinv1.MeshCredentials) { c.MeshId = strings.Repeat("m", 33) }, wantErr: true},
		{name: "32 char mesh id", mutate: func(c *meshjoinv1.MeshCredentials) { c.MeshId = strings.Repeat("m", 32) }},
		{name: "7 char passphrase", mutate: func(c *meshjoinv1.MeshCredentials) { c.Passphrase = "1234567" }, wantErr: true},
		{name: "8 char passphrase", mutate: func(c *meshjoinv1.MeshCredentials) { c.Passphrase = "12345678" }},
		{name: "63 char passphrase", mutate: func(c *meshjoinv1.MeshCredentials) { c.Passphrase = strings.Repeat("p", 63) }},
		{name: "64 char passphrase", mutate: func(c *meshjoinv1.MeshCredentials) { c.Passphrase = strings.Repeat("p", 64) }, wantErr: true},
		{name: "psk2 rejected", mutate: func(c *meshjoinv1.MeshCredentials) { c.Encryption = wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK2 }, wantErr: true},
		{name: "unspecified encryption rejected", mutate: func(c *meshjoinv1.MeshCredentials) {
			c.Encryption = wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_UNSPECIFIED
		}, wantErr: true},
		{name: "bandwidth 3 rejected", mutate: func(c *meshjoinv1.MeshCredentials) { c.BandwidthMhz = 3 }, wantErr: true},
		{name: "bandwidth 40 accepted", mutate: func(c *meshjoinv1.MeshCredentials) { c.BandwidthMhz = 40 }},
		{name: "channel 0 rejected", mutate: func(c *meshjoinv1.MeshCredentials) { c.Channel = 0 }, wantErr: true},
		{name: "empty country accepted", mutate: func(c *meshjoinv1.MeshCredentials) { c.CountryCode = "" }},
		{name: "lowercase country rejected", mutate: func(c *meshjoinv1.MeshCredentials) { c.CountryCode = "us" }, wantErr: true},
		{name: "four letter country rejected", mutate: func(c *meshjoinv1.MeshCredentials) { c.CountryCode = "ABCD" }, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validMeshCredentials()
			tc.mutate(c)

			err := v.Validate(c)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidation_MeshJoinPayload_RequiresHalow(t *testing.T) {
	v := newValidator(t)

	assert.Error(t, v.Validate(&meshjoinv1.MeshJoinPayload{SourceHostname: "alpha"}),
		"halow credentials are required")
	assert.NoError(t, v.Validate(&meshjoinv1.MeshJoinPayload{SourceHostname: "alpha", Halow: validMeshCredentials()}),
		"backhaul is optional")
	assert.Error(t, v.Validate(&meshjoinv1.MeshJoinPayload{SourceHostname: strings.Repeat("h", 64), Halow: validMeshCredentials()}),
		"hostname is capped at 63")
}

func TestValidation_ApplyMeshJoinRequest(t *testing.T) {
	v := newValidator(t)

	assert.Error(t, v.Validate(&meshjoinv1.ApplyMeshJoinRequest{}), "payload is required")
	assert.NoError(t, v.Validate(&meshjoinv1.ApplyMeshJoinRequest{
		Payload:    &meshjoinv1.MeshJoinPayload{Halow: validMeshCredentials()},
		HalowRadio: "radio3",
	}))
	assert.Error(t, v.Validate(&meshjoinv1.ApplyMeshJoinRequest{
		Payload:       &meshjoinv1.MeshJoinPayload{Halow: validMeshCredentials()},
		BackhaulRadio: strings.Repeat("r", 33),
	}), "radio names are capped at 32")
}

// ── MeshBackhaulProfile ──────────────────────────────────────────────────────

func TestValidation_MeshBackhaulProfile_RadioFields(t *testing.T) {
	v := newValidator(t)

	base := func() *setupv1.MeshBackhaulProfile {
		return &setupv1.MeshBackhaulProfile{MeshId: "backhaul-2g", Passphrase: "backhaulpass"}
	}

	tests := []struct {
		name    string
		mutate  func(p *setupv1.MeshBackhaulProfile)
		wantErr bool
	}{
		{name: "zero values keep defaults", mutate: func(*setupv1.MeshBackhaulProfile) {}},
		{name: "20 MHz", mutate: func(p *setupv1.MeshBackhaulProfile) { p.BandwidthMhz = 20 }},
		{name: "40 MHz", mutate: func(p *setupv1.MeshBackhaulProfile) { p.BandwidthMhz = 40 }},
		{name: "80 MHz rejected", mutate: func(p *setupv1.MeshBackhaulProfile) { p.BandwidthMhz = 80 }, wantErr: true},
		{name: "channel 11", mutate: func(p *setupv1.MeshBackhaulProfile) { p.Channel = 11 }},
		{name: "channel 12 rejected", mutate: func(p *setupv1.MeshBackhaulProfile) { p.Channel = 12 }, wantErr: true},
		{name: "country GB", mutate: func(p *setupv1.MeshBackhaulProfile) { p.CountryCode = "GB" }},
		{name: "lowercase country rejected", mutate: func(p *setupv1.MeshBackhaulProfile) { p.CountryCode = "gb" }, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := base()
			tc.mutate(p)

			err := v.Validate(p)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// ── UpdateRadioSettingsRequest.settings.mesh_rssi_threshold ──────────────────

func radioSettingsWithFloor(dbm *int32) *wificonfigv1.UpdateRadioSettingsRequest {
	return &wificonfigv1.UpdateRadioSettingsRequest{
		RadioName: "radio2",
		Settings: &wificonfigv1.RadioSettings{
			Ssid:              "openmanet",
			MeshRssiThreshold: dbm,
		},
	}
}

func TestValidation_UpdateRadioSettingsRequest_MeshRssiThresholdOutOfRange(t *testing.T) {
	v := newValidator(t)

	for _, dbm := range []int32{-86, -69, 0, -255} {
		dbm := dbm
		t.Run(fmt.Sprintf("dbm_%d", dbm), func(t *testing.T) {
			err := v.Validate(radioSettingsWithFloor(proto.Int32(dbm)))
			assert.Error(t, err, "mesh_rssi_threshold %d is outside [-85,-70] and must fail validation", dbm)
		})
	}
}

func TestValidation_UpdateRadioSettingsRequest_MeshRssiThresholdInRange(t *testing.T) {
	v := newValidator(t)

	for _, dbm := range []int32{-85, -80, -70} {
		dbm := dbm
		t.Run(fmt.Sprintf("dbm_%d", dbm), func(t *testing.T) {
			err := v.Validate(radioSettingsWithFloor(proto.Int32(dbm)))
			assert.NoError(t, err, "mesh_rssi_threshold %d must pass validation", dbm)
		})
	}
}

func TestValidation_UpdateRadioSettingsRequest_MeshRssiThresholdUnset(t *testing.T) {
	v := newValidator(t)

	err := v.Validate(radioSettingsWithFloor(nil))
	assert.NoError(t, err, "unset leaves UCI alone and must validate")
}
