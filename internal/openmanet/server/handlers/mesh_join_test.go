package handlers_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	meshjoinv1 "github.com/openmanet/openmanetd/internal/api/openmanet/mesh_join/v1"
	wificonfigv1 "github.com/openmanet/openmanetd/internal/api/openmanet/wifi_config/v1"
	"github.com/openmanet/openmanetd/internal/meshjoin"
	"github.com/openmanet/openmanetd/internal/network/morseregdb"
	"github.com/openmanet/openmanetd/internal/openmanet/server/handlers"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

// newMeshJoinReader seeds a HaLow mesh radio (radio3), a 2.4 GHz radio
// carrying the batmesh1 backhaul (radio0) and a 2.4 GHz client AP
// (radio2) — the shape a configured mesh-point extender has after the
// wizard ran with a backhaul.
func newMeshJoinReader() *fakeConfigReader {
	return &fakeConfigReader{
		data: map[string]map[string]map[string][]string{
			"wireless": {
				"radio0": {
					"type":    {"mac80211"},
					"band":    {"2g"},
					"channel": {"8"},
					"htmode":  {"HE20"},
					"country": {"US"},
					"txpower": {"20"},
				},
				"radio2": {
					"type":    {"mac80211"},
					"band":    {"5g"},
					"channel": {"36"},
					"htmode":  {"VHT80"},
					"country": {"US"},
					"txpower": {"20"},
				},
				"radio3": {
					"type":    {"morse"},
					"band":    {"s1g"},
					"channel": {"44"},
					"htmode":  {"8 MHz"},
					"country": {"US"},
					"txpower": {"14"},
				},
				"batmesh1_radio0": {
					"device":     {"radio0"},
					"network":    {"batmesh1"},
					"mode":       {"mesh"},
					"mesh_id":    {"field-mesh-2g"},
					"key":        {"backhaul-pass"},
					"encryption": {"sae"},
				},
				"default_radio2": {
					"device":     {"radio2"},
					"network":    {"ahwlan"},
					"mode":       {"ap"},
					"ssid":       {"openmanet"},
					"key":        {"clientsecret"},
					"encryption": {"psk2"},
				},
				"default_radio3": {
					"device":     {"radio3"},
					"network":    {"batmesh0"},
					"mode":       {"mesh"},
					"ssid":       {"field-mesh"},
					"mesh_id":    {"field-mesh"},
					"key":        {"correct-horse"},
					"encryption": {"sae"},
				},
			},
		},
		sectionTypes: map[string]map[string]string{
			"wireless": {
				"radio0":          "wifi-device",
				"radio2":          "wifi-device",
				"radio3":          "wifi-device",
				"batmesh1_radio0": "wifi-iface",
				"default_radio2":  "wifi-iface",
				"default_radio3":  "wifi-iface",
			},
		},
	}
}

func newTestMeshJoinService(reader *fakeConfigReader) *handlers.MeshJoinService {
	return &handlers.MeshJoinService{
		Log:          zerolog.Nop(),
		ConfigReader: reader,
		Hostname:     func() (string, error) { return "alpha", nil },
	}
}

func TestGetMeshJoinQR_HalowAndBackhaul(t *testing.T) {
	svc := newTestMeshJoinService(newMeshJoinReader())

	resp, err := svc.GetMeshJoinQR(context.Background(), &emptypb.Empty{})
	require.NoError(t, err)

	p := resp.GetPayload()
	assert.Equal(t, "alpha", p.GetSourceHostname())

	h := p.GetHalow()
	require.NotNil(t, h)
	assert.Equal(t, "field-mesh", h.GetMeshId())
	assert.Equal(t, "correct-horse", h.GetPassphrase())
	assert.Equal(t, wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE, h.GetEncryption())
	assert.Equal(t, uint32(8), h.GetBandwidthMhz())
	assert.Equal(t, uint32(44), h.GetChannel())
	assert.Equal(t, "US", h.GetCountryCode())

	b := p.GetBackhaul()
	require.NotNil(t, b, "the batmesh1 iface is the backhaul")
	assert.Equal(t, "field-mesh-2g", b.GetMeshId())
	assert.Equal(t, "backhaul-pass", b.GetPassphrase())
	assert.Equal(t, uint32(20), b.GetBandwidthMhz())
	assert.Equal(t, uint32(8), b.GetChannel())

	assert.True(t, strings.HasPrefix(resp.GetPayloadText(), meshjoin.Prefix))
	assert.True(t, strings.HasPrefix(resp.GetSvg(), "<svg "))

	decoded, err := meshjoin.Decode(resp.GetPayloadText())
	require.NoError(t, err)
	assert.Equal(t, "field-mesh", decoded.GetHalow().GetMeshId())
}

func TestGetMeshJoinQR_HalowOnly(t *testing.T) {
	reader := newMeshJoinReader()
	require.NoError(t, reader.DelSection("wireless", "batmesh1_radio0"))

	svc := newTestMeshJoinService(reader)

	resp, err := svc.GetMeshJoinQR(context.Background(), &emptypb.Empty{})
	require.NoError(t, err)
	assert.Nil(t, resp.GetPayload().GetBackhaul(), "no 2.4 GHz mesh iface → no backhaul")
	assert.Equal(t, "field-mesh", resp.GetPayload().GetHalow().GetMeshId())
}

func TestGetMeshJoinQR_BackhaulOnNonBatmesh1Iface(t *testing.T) {
	reader := newMeshJoinReader()
	// Move the backhaul to a plain default_ section on a different
	// network name; it must still be picked up as the backhaul.
	require.NoError(t, reader.DelSection("wireless", "batmesh1_radio0"))
	require.NoError(t, reader.AddSection("wireless", "default_radio0", "wifi-iface"))

	for k, v := range map[string]string{
		"device": "radio0", "network": "batmesh0", "mode": "mesh",
		"mesh_id": "other-2g", "key": "backhaul-pass", "encryption": "sae",
	} {
		require.NoError(t, reader.SetType("wireless", "default_radio0", k, 0, v))
	}

	svc := newTestMeshJoinService(reader)

	resp, err := svc.GetMeshJoinQR(context.Background(), &emptypb.Empty{})
	require.NoError(t, err)
	assert.Equal(t, "other-2g", resp.GetPayload().GetBackhaul().GetMeshId())
}

func TestGetMeshJoinQR_DisabledIfaceIgnored(t *testing.T) {
	reader := newMeshJoinReader()
	require.NoError(t, reader.SetType("wireless", "batmesh1_radio0", "disabled", 0, "1"))

	svc := newTestMeshJoinService(reader)

	resp, err := svc.GetMeshJoinQR(context.Background(), &emptypb.Empty{})
	require.NoError(t, err)
	assert.Nil(t, resp.GetPayload().GetBackhaul())
}

func TestGetMeshJoinQR_NoHalowMesh(t *testing.T) {
	reader := newMeshJoinReader()
	require.NoError(t, reader.DelSection("wireless", "default_radio3"))

	svc := newTestMeshJoinService(reader)

	_, err := svc.GetMeshJoinQR(context.Background(), &emptypb.Empty{})

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, connect.CodeFailedPrecondition, cerr.Code())
	assert.Contains(t, cerr.Message(), "no HaLow mesh interface")
}

func TestGetMeshJoinQR_OpenHalowMeshRejected(t *testing.T) {
	reader := newMeshJoinReader()
	require.NoError(t, reader.SetType("wireless", "default_radio3", "encryption", 0, "none"))
	require.NoError(t, reader.Del("wireless", "default_radio3", "key"))

	svc := newTestMeshJoinService(reader)

	_, err := svc.GetMeshJoinQR(context.Background(), &emptypb.Empty{})

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, connect.CodeFailedPrecondition, cerr.Code())
	assert.Contains(t, cerr.Message(), "WPA3")
}

func TestGetMeshJoinQR_NonSAEBackhaulSkipped(t *testing.T) {
	reader := newMeshJoinReader()
	require.NoError(t, reader.SetType("wireless", "batmesh1_radio0", "encryption", 0, "psk2"))

	svc := newTestMeshJoinService(reader)

	resp, err := svc.GetMeshJoinQR(context.Background(), &emptypb.Empty{})
	require.NoError(t, err, "the HaLow mesh still shares")
	assert.Nil(t, resp.GetPayload().GetBackhaul())
}

func TestGetMeshJoinQR_UnknownHTMode(t *testing.T) {
	reader := newMeshJoinReader()
	require.NoError(t, reader.SetType("wireless", "radio3", "htmode", 0, "EHT320"))

	svc := newTestMeshJoinService(reader)

	_, err := svc.GetMeshJoinQR(context.Background(), &emptypb.Empty{})

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, connect.CodeInternal, cerr.Code())
}

func TestGetMeshJoinQR_InvalidCountryRejectedByValidator(t *testing.T) {
	// A malformed country (digit) survives credentialsFromIface (which
	// uppercases it and does not check the country) but must be caught by
	// the assembled-payload protovalidate pass, which enforces the
	// country_code pattern ^([A-Z]{2,3})?$.
	reader := newMeshJoinReader()
	require.NoError(t, reader.SetType("wireless", "radio3", "country", 0, "us1"))

	svc := newTestMeshJoinService(reader)

	_, err := svc.GetMeshJoinQR(context.Background(), &emptypb.Empty{})

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, connect.CodeInternal, cerr.Code())
}

func TestGetMeshJoinQR_HostnameError(t *testing.T) {
	svc := newTestMeshJoinService(newMeshJoinReader())
	svc.Hostname = func() (string, error) { return "", errors.New("gethostname: boom") }

	_, err := svc.GetMeshJoinQR(context.Background(), &emptypb.Empty{})

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, connect.CodeInternal, cerr.Code())
}

func TestGetMeshJoinQR_HostnameTruncatedTo63(t *testing.T) {
	svc := newTestMeshJoinService(newMeshJoinReader())
	svc.Hostname = func() (string, error) { return strings.Repeat("h", 80), nil }

	resp, err := svc.GetMeshJoinQR(context.Background(), &emptypb.Empty{})
	require.NoError(t, err)
	assert.Len(t, resp.GetPayload().GetSourceHostname(), 63)
}

func TestGetMeshJoinQR_NeverLogsSecrets(t *testing.T) {
	var buf bytes.Buffer

	svc := newTestMeshJoinService(newMeshJoinReader())
	svc.Log = zerolog.New(&buf).Level(zerolog.TraceLevel)

	resp, err := svc.GetMeshJoinQR(context.Background(), &emptypb.Empty{})
	require.NoError(t, err)

	logs := buf.String()
	assert.NotContains(t, logs, "correct-horse")
	assert.NotContains(t, logs, "backhaul-pass")
	assert.NotContains(t, logs, resp.GetPayloadText())
}

// fakeRadioApplier records batch applies and serves canned current
// settings, standing in for WifiConfigService.
type fakeRadioApplier struct {
	currentErr error
	applyErr   error
	current    map[string]*wificonfigv1.RadioSettings
	applied    [][]handlers.RadioSettingsUpdate
	mu         sync.Mutex
}

func (f *fakeRadioApplier) CurrentRadioSettings(radioName string) (*wificonfigv1.RadioSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.currentErr != nil {
		return nil, f.currentErr
	}

	s, ok := f.current[radioName]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no interface linked to radio %q", radioName))
	}

	// Return a copy so the handler's overlay never mutates the fixture.
	cp, ok := proto.Clone(s).(*wificonfigv1.RadioSettings)
	if !ok {
		return nil, fmt.Errorf("clone radio settings for %q: unexpected type", radioName)
	}

	return cp, nil
}

func (f *fakeRadioApplier) ApplyRadioSettingsBatch(_ context.Context, updates []handlers.RadioSettingsUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.applied = append(f.applied, updates)

	return f.applyErr
}

func (f *fakeRadioApplier) batches() [][]handlers.RadioSettingsUpdate {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.applied
}

func newFakeRadioApplier() *fakeRadioApplier {
	return &fakeRadioApplier{current: map[string]*wificonfigv1.RadioSettings{
		"radio0": {Ssid: "old-2g", Channel: "8", Bandwidth: wificonfigv1.WifiHTMode_WIFI_HT_MODE_HE20, TxPower: 20, Mode: wificonfigv1.WifiMode_WIFI_MODE_MESH, Country: strPtr("US")},
		"radio2": {Ssid: "openmanet", Channel: "36", Bandwidth: wificonfigv1.WifiHTMode_WIFI_HT_MODE_VHT80, TxPower: 20, Mode: wificonfigv1.WifiMode_WIFI_MODE_AP, Country: strPtr("US")},
		"radio3": {Ssid: "old-mesh", Channel: "44", Bandwidth: wificonfigv1.WifiHTMode_WIFI_HT_MODE_S1G_8MHZ, TxPower: 14, Mode: wificonfigv1.WifiMode_WIFI_MODE_MESH, Country: strPtr("US")},
	}}
}

func joinPayload() *meshjoinv1.MeshJoinPayload {
	return &meshjoinv1.MeshJoinPayload{
		SourceHostname: "alpha",
		Halow: &meshjoinv1.MeshCredentials{
			MeshId:       "field-mesh",
			Passphrase:   "correct-horse",
			Encryption:   wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE,
			BandwidthMhz: 8,
			Channel:      28,
			CountryCode:  "US",
		},
		Backhaul: &meshjoinv1.MeshCredentials{
			MeshId:       "field-mesh-2g",
			Passphrase:   "backhaul-pass",
			Encryption:   wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE,
			BandwidthMhz: 40,
			Channel:      6,
			CountryCode:  "US",
		},
	}
}

// newTestMeshJoinApplyService wires the seeded reader, a fake applier
// and the regdb fixture (US: 8 MHz → 12/28/44; EU: 1 and 2 MHz only).
func newTestMeshJoinApplyService(t *testing.T, reader *fakeConfigReader) (*handlers.MeshJoinService, *fakeRadioApplier) {
	t.Helper()

	applier := newFakeRadioApplier()
	svc := newTestMeshJoinService(reader)
	svc.Radios = applier
	svc.RegDBPath = regdbFixturePath(t)

	return svc, applier
}

func findUpdate(t *testing.T, batch []handlers.RadioSettingsUpdate, radio string) *wificonfigv1.RadioSettings {
	t.Helper()

	for _, u := range batch {
		if u.RadioName == radio {
			return u.Settings
		}
	}

	t.Fatalf("no update for %s in batch", radio)

	return nil
}

func TestApplyMeshJoin_HalowAndBackhaul(t *testing.T) {
	svc, applier := newTestMeshJoinApplyService(t, newMeshJoinReader())

	resp, err := svc.ApplyMeshJoin(context.Background(), &meshjoinv1.ApplyMeshJoinRequest{Payload: joinPayload()})
	require.NoError(t, err)

	batches := applier.batches()
	require.Len(t, batches, 1, "both radios go in one batch")
	require.Len(t, batches[0], 2)

	halow := findUpdate(t, batches[0], "radio3")
	assert.Equal(t, wificonfigv1.WifiMode_WIFI_MODE_MESH, halow.GetMode())
	assert.Equal(t, "field-mesh", halow.GetMeshId())
	assert.Equal(t, "field-mesh", halow.GetSsid(), "ssid mirrors the mesh id so validation passes")
	assert.Equal(t, "correct-horse", halow.GetPassword())
	assert.Equal(t, wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_SAE, halow.GetEncryption())
	assert.Equal(t, "28", halow.GetChannel())
	assert.Equal(t, wificonfigv1.WifiHTMode_WIFI_HT_MODE_S1G_8MHZ, halow.GetBandwidth())
	assert.Equal(t, "US", halow.GetCountry())
	assert.Equal(t, int32(14), halow.GetTxPower(), "tx power carried over from current settings")
	assert.False(t, halow.GetDisabled())

	bh := findUpdate(t, batches[0], "radio0")
	assert.Equal(t, "field-mesh-2g", bh.GetMeshId())
	assert.Equal(t, "6", bh.GetChannel())
	assert.Equal(t, wificonfigv1.WifiHTMode_WIFI_HT_MODE_HE40, bh.GetBandwidth(), "40 MHz backhaul is HE40")
	assert.Equal(t, int32(20), bh.GetTxPower())

	require.Len(t, resp.GetRadios(), 2)
	assert.Equal(t, "radio3", resp.GetRadios()[0].GetRadioName())
	assert.Equal(t, meshjoinv1.MeshJoinRadioRole_MESH_JOIN_RADIO_ROLE_HALOW, resp.GetRadios()[0].GetRole())
	assert.Equal(t, meshjoinv1.MeshJoinRadioStatus_MESH_JOIN_RADIO_STATUS_APPLIED, resp.GetRadios()[0].GetStatus())
	assert.Equal(t, "radio0", resp.GetRadios()[1].GetRadioName())
	assert.Equal(t, meshjoinv1.MeshJoinRadioRole_MESH_JOIN_RADIO_ROLE_BACKHAUL, resp.GetRadios()[1].GetRole())
	assert.Equal(t, meshjoinv1.MeshJoinRadioStatus_MESH_JOIN_RADIO_STATUS_APPLIED, resp.GetRadios()[1].GetStatus())
}

func TestApplyMeshJoin_HalowOnly(t *testing.T) {
	svc, applier := newTestMeshJoinApplyService(t, newMeshJoinReader())

	p := joinPayload()
	p.Backhaul = nil

	resp, err := svc.ApplyMeshJoin(context.Background(), &meshjoinv1.ApplyMeshJoinRequest{Payload: p})
	require.NoError(t, err)

	require.Len(t, applier.batches(), 1)
	assert.Len(t, applier.batches()[0], 1)
	require.Len(t, resp.GetRadios(), 1)
	assert.Equal(t, "radio3", resp.GetRadios()[0].GetRadioName())
}

func TestApplyMeshJoin_BackhaulSkippedWhenNoMeshModeRadio(t *testing.T) {
	reader := newMeshJoinReader()
	require.NoError(t, reader.SetType("wireless", "batmesh1_radio0", "mode", 0, "ap"))

	svc, applier := newTestMeshJoinApplyService(t, reader)

	resp, err := svc.ApplyMeshJoin(context.Background(), &meshjoinv1.ApplyMeshJoinRequest{Payload: joinPayload()})
	require.NoError(t, err)

	require.Len(t, applier.batches(), 1)
	assert.Len(t, applier.batches()[0], 1, "only the HaLow radio is written")

	require.Len(t, resp.GetRadios(), 2)
	assert.Equal(t, meshjoinv1.MeshJoinRadioStatus_MESH_JOIN_RADIO_STATUS_SKIPPED, resp.GetRadios()[1].GetStatus())
	assert.Contains(t, resp.GetRadios()[1].GetReason(), "mesh mode")
}

func TestApplyMeshJoin_BackhaulOverrideTargetsAPRadio(t *testing.T) {
	svc, applier := newTestMeshJoinApplyService(t, newMeshJoinReader())

	resp, err := svc.ApplyMeshJoin(context.Background(), &meshjoinv1.ApplyMeshJoinRequest{
		Payload:       joinPayload(),
		BackhaulRadio: "radio2",
	})
	require.NoError(t, err)

	bh := findUpdate(t, applier.batches()[0], "radio2")
	assert.Equal(t, wificonfigv1.WifiMode_WIFI_MODE_MESH, bh.GetMode(), "an AP radio named explicitly is switched to mesh")
	assert.Equal(t, "radio2", resp.GetRadios()[1].GetRadioName())
}

func TestApplyMeshJoin_MorseRadioAsBackhaulRejected(t *testing.T) {
	svc, applier := newTestMeshJoinApplyService(t, newMeshJoinReader())

	_, err := svc.ApplyMeshJoin(context.Background(), &meshjoinv1.ApplyMeshJoinRequest{
		Payload:       joinPayload(),
		BackhaulRadio: "radio3",
	})

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, connect.CodeInvalidArgument, cerr.Code())
	assert.Empty(t, applier.batches())
}

func TestApplyMeshJoin_NonMorseHalowOverrideRejected(t *testing.T) {
	svc, applier := newTestMeshJoinApplyService(t, newMeshJoinReader())

	_, err := svc.ApplyMeshJoin(context.Background(), &meshjoinv1.ApplyMeshJoinRequest{
		Payload:    joinPayload(),
		HalowRadio: "radio0",
	})

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, connect.CodeInvalidArgument, cerr.Code())
	assert.Contains(t, cerr.Message(), "radio0")
	assert.Empty(t, applier.batches())
}

func TestApplyMeshJoin_NoHalowRadio(t *testing.T) {
	reader := newMeshJoinReader()
	require.NoError(t, reader.DelSection("wireless", "radio3"))
	require.NoError(t, reader.DelSection("wireless", "default_radio3"))

	svc, _ := newTestMeshJoinApplyService(t, reader)

	_, err := svc.ApplyMeshJoin(context.Background(), &meshjoinv1.ApplyMeshJoinRequest{Payload: joinPayload()})

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, connect.CodeFailedPrecondition, cerr.Code())
}

func TestApplyMeshJoin_TwoMorseRadiosNeedOverride(t *testing.T) {
	reader := newMeshJoinReader()
	require.NoError(t, reader.AddSection("wireless", "radio4", "wifi-device"))
	require.NoError(t, reader.SetType("wireless", "radio4", "type", 0, "morse"))

	svc, _ := newTestMeshJoinApplyService(t, reader)

	_, err := svc.ApplyMeshJoin(context.Background(), &meshjoinv1.ApplyMeshJoinRequest{Payload: joinPayload()})

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, connect.CodeInvalidArgument, cerr.Code())
	assert.Contains(t, cerr.Message(), "halow_radio")
}

func TestApplyMeshJoin_IllegalHalowChannelPerRegdb(t *testing.T) {
	svc, applier := newTestMeshJoinApplyService(t, newMeshJoinReader())

	p := joinPayload()
	p.Halow.CountryCode = "EU" // EU allows 1 and 2 MHz only in the fixture
	p.Halow.BandwidthMhz = 8
	p.Halow.Channel = 44

	_, err := svc.ApplyMeshJoin(context.Background(), &meshjoinv1.ApplyMeshJoinRequest{Payload: p})

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, connect.CodeInvalidArgument, cerr.Code())
	assert.Contains(t, cerr.Message(), "radio3")
	assert.Contains(t, cerr.Message(), "channel 44")
	assert.Contains(t, cerr.Message(), "EU")
	assert.Empty(t, applier.batches(), "nothing is written when validation fails")
}

func TestApplyMeshJoin_MultipleIssuesReportedTogether(t *testing.T) {
	svc, _ := newTestMeshJoinApplyService(t, newMeshJoinReader())

	p := joinPayload()
	p.Halow.CountryCode = "EU"
	p.Backhaul.Channel = 14

	_, err := svc.ApplyMeshJoin(context.Background(), &meshjoinv1.ApplyMeshJoinRequest{Payload: p})

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	assert.Contains(t, cerr.Message(), "radio3")
	assert.Contains(t, cerr.Message(), "radio0")
	assert.Contains(t, cerr.Message(), "; ")
}

func TestApplyMeshJoin_RegdbMissingFallsBackToStaticList(t *testing.T) {
	svc, applier := newTestMeshJoinApplyService(t, newMeshJoinReader())
	svc.RegDBPath = filepath.Join(t.TempDir(), "missing.csv")

	p := joinPayload()
	p.Halow.CountryCode = "EU"

	_, err := svc.ApplyMeshJoin(context.Background(), &meshjoinv1.ApplyMeshJoinRequest{Payload: p})
	require.NoError(t, err, "without a regdb any S1G channel is accepted")
	assert.Len(t, applier.batches(), 1)

	p.Halow.Channel = 99

	_, err = svc.ApplyMeshJoin(context.Background(), &meshjoinv1.ApplyMeshJoinRequest{Payload: p})

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, connect.CodeInvalidArgument, cerr.Code())
}

func TestApplyMeshJoin_RegdbLoadErrorIsInternal(t *testing.T) {
	svc, _ := newTestMeshJoinApplyService(t, newMeshJoinReader())
	svc.LoadRegDB = func(string) (*morseregdb.DB, error) { return nil, errors.New("csv: parse error") }

	_, err := svc.ApplyMeshJoin(context.Background(), &meshjoinv1.ApplyMeshJoinRequest{Payload: joinPayload()})

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, connect.CodeInternal, cerr.Code())
}

func TestApplyMeshJoin_NonSAERejected(t *testing.T) {
	svc, _ := newTestMeshJoinApplyService(t, newMeshJoinReader())

	p := joinPayload()
	p.Halow.Encryption = wificonfigv1.WifiEncryption_WIFI_ENCRYPTION_PSK2

	_, err := svc.ApplyMeshJoin(context.Background(), &meshjoinv1.ApplyMeshJoinRequest{Payload: p})

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, connect.CodeInvalidArgument, cerr.Code())
	assert.Contains(t, cerr.Message(), "SAE")
}

func TestApplyMeshJoin_BatchErrorIsInternal(t *testing.T) {
	svc, applier := newTestMeshJoinApplyService(t, newMeshJoinReader())
	applier.applyErr = errors.New("reload wireless: boom")

	_, err := svc.ApplyMeshJoin(context.Background(), &meshjoinv1.ApplyMeshJoinRequest{Payload: joinPayload()})

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, connect.CodeInternal, cerr.Code())
}

func TestApplyMeshJoin_NoRadiosDependency(t *testing.T) {
	svc := newTestMeshJoinService(newMeshJoinReader())

	_, err := svc.ApplyMeshJoin(context.Background(), &meshjoinv1.ApplyMeshJoinRequest{Payload: joinPayload()})

	var cerr *connect.Error
	require.ErrorAs(t, err, &cerr)
	assert.Equal(t, connect.CodeUnavailable, cerr.Code())
}

func TestApplyMeshJoin_NeverLogsSecrets(t *testing.T) {
	var buf bytes.Buffer

	svc, _ := newTestMeshJoinApplyService(t, newMeshJoinReader())
	svc.Log = zerolog.New(&buf).Level(zerolog.TraceLevel)

	_, err := svc.ApplyMeshJoin(context.Background(), &meshjoinv1.ApplyMeshJoinRequest{Payload: joinPayload()})
	require.NoError(t, err)

	assert.NotContains(t, buf.String(), "correct-horse")
	assert.NotContains(t, buf.String(), "backhaul-pass")
}
