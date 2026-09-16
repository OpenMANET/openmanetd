package handlers

import (
	"errors"
	"strings"
	"testing"

	"github.com/digineo/go-uci/v2"
	"github.com/openmanet/openmanetd/internal/network"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
)

// internalFakeReader is a minimal in-memory ConfigReader used to exercise
// findUnusedBatmesh directly (an unexported helper that cannot be reached
// from the external handlers_test package).
type internalFakeReader struct {
	data         map[string]map[string]map[string][]string
	sectionTypes map[string]map[string]string
}

func newInternalFakeReader() *internalFakeReader {
	return &internalFakeReader{
		data:         map[string]map[string]map[string][]string{},
		sectionTypes: map[string]map[string]string{},
	}
}

func (f *internalFakeReader) Get(config, section, option string) ([]string, bool) {
	if cfg, ok := f.data[config]; ok {
		if sec, ok := cfg[section]; ok {
			if vals, ok := sec[option]; ok {
				return vals, true
			}
		}
	}

	return nil, false
}

func (f *internalFakeReader) GetSections(config, secType string) ([]string, error) {
	var out []string

	if typeMap, ok := f.sectionTypes[config]; ok {
		for sec, t := range typeMap {
			if t == secType {
				out = append(out, sec)
			}
		}
	}

	return out, nil
}

func (f *internalFakeReader) SetType(config, section, option string, _ uci.OptionType, values ...string) error {
	if f.data[config] == nil {
		f.data[config] = map[string]map[string][]string{}
	}

	if f.data[config][section] == nil {
		f.data[config][section] = map[string][]string{}
	}

	f.data[config][section][option] = values

	return nil
}

func (f *internalFakeReader) Del(config, section, option string) error {
	if f.data[config] != nil && f.data[config][section] != nil {
		delete(f.data[config][section], option)
	}

	return nil
}

func (f *internalFakeReader) AddSection(config, section, typ string) error {
	if f.sectionTypes[config] == nil {
		f.sectionTypes[config] = map[string]string{}
	}

	f.sectionTypes[config][section] = typ

	if f.data[config] == nil {
		f.data[config] = map[string]map[string][]string{}
	}

	if f.data[config][section] == nil {
		f.data[config][section] = map[string][]string{}
	}

	return nil
}

func (f *internalFakeReader) DelSection(config, section string) error {
	if f.sectionTypes[config] != nil {
		delete(f.sectionTypes[config], section)
	}

	if f.data[config] != nil {
		delete(f.data[config], section)
	}

	return nil
}

func (f *internalFakeReader) Commit() error       { return nil }
func (f *internalFakeReader) ReloadConfig() error { return nil }

// addIface seeds a wifi-iface section with a network binding.
func addIface(t *testing.T, r *internalFakeReader, name, networkVal string) {
	t.Helper()

	if err := r.AddSection("wireless", name, "wifi-iface"); err != nil {
		t.Fatalf("AddSection %s: %v", name, err)
	}

	if networkVal == "" {
		return
	}

	if err := r.SetType("wireless", name, "network", uci.TypeOption, networkVal); err != nil {
		t.Fatalf("SetType %s.network: %v", name, err)
	}
}

func TestFindUnusedBatmesh(t *testing.T) {
	tests := []struct {
		name         string
		seed         func(*internalFakeReader)
		excludeIface string
		want         string
		wantErr      bool
	}{
		{
			name:         "no ifaces — picks batmesh0",
			seed:         func(*internalFakeReader) {},
			excludeIface: "default_radio0",
			want:         network.BatmanPrimaryIface,
		},
		{
			name: "batmesh0 taken — picks batmesh1",
			seed: func(r *internalFakeReader) {
				addIface(t, r, "default_radio0", "batmesh0")
			},
			excludeIface: "default_radio1",
			want:         network.BatmanSecondaryIface,
		},
		{
			name: "both taken — error",
			seed: func(r *internalFakeReader) {
				addIface(t, r, "default_radio0", "batmesh0")
				addIface(t, r, "default_radio1", "batmesh1")
			},
			excludeIface: "default_radio2",
			wantErr:      true,
		},
		{
			name: "excludeIface holds batmesh0 — picks batmesh0 again",
			seed: func(r *internalFakeReader) {
				addIface(t, r, "default_radio0", "batmesh0")
			},
			excludeIface: "default_radio0",
			want:         network.BatmanPrimaryIface,
		},
		{
			name: "iface with no network option is ignored",
			seed: func(r *internalFakeReader) {
				addIface(t, r, "default_radio0", "")
			},
			excludeIface: "default_radio1",
			want:         network.BatmanPrimaryIface,
		},
		{
			name: "ahwlan-bound ifaces don't count against the pool",
			seed: func(r *internalFakeReader) {
				addIface(t, r, "default_radio0", "ahwlan")
				addIface(t, r, "meshap_radio0", "ahwlan")
			},
			excludeIface: "default_radio1",
			want:         network.BatmanPrimaryIface,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newInternalFakeReader()
			tt.seed(r)

			got, err := findUnusedBatmesh(r, tt.excludeIface)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (got=%q)", got)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindUnusedBatmesh_GetSectionsErrorPropagates(t *testing.T) {
	// Simulate a reader whose GetSections fails by wrapping the in-memory
	// reader and returning an error.
	r := &errReader{inner: newInternalFakeReader(), err: errors.New("boom")}

	_, err := findUnusedBatmesh(r, "default_radio0")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

type errReader struct {
	inner  *internalFakeReader
	err    error
	delErr error
}

func (e *errReader) Get(config, section, option string) ([]string, bool) {
	return e.inner.Get(config, section, option)
}

func (e *errReader) GetSections(string, string) ([]string, error) { return nil, e.err }
func (e *errReader) SetType(config, section, option string, t uci.OptionType, values ...string) error {
	return e.inner.SetType(config, section, option, t, values...)
}
func (e *errReader) Del(config, section, option string) error {
	if e.delErr != nil {
		return e.delErr
	}

	return e.inner.Del(config, section, option)
}
func (e *errReader) AddSection(config, section, typ string) error {
	return e.inner.AddSection(config, section, typ)
}
func (e *errReader) DelSection(config, section string) error {
	return e.inner.DelSection(config, section)
}
func (e *errReader) Commit() error       { return nil }
func (e *errReader) ReloadConfig() error { return nil }

// TestClearMeshOnlyOptions_DelErrorReturnsStageWriteError pins
// clearMeshOnlyOptions' error path: a failing Del must surface as a
// *stageWriteError (UpdateRadioSettings' historical Success=false
// contract) naming the option that failed to clear, not a bare RPC error.
func TestClearMeshOnlyOptions_DelErrorReturnsStageWriteError(t *testing.T) {
	r := &errReader{inner: newInternalFakeReader(), delErr: errors.New("boom")}
	svc := &WifiConfigService{ConfigReader: r, Log: zerolog.Nop()}

	err := svc.clearMeshOnlyOptions("default_radio2")

	var sw *stageWriteError
	if !errors.As(err, &sw) {
		t.Fatalf("expected *stageWriteError, got %T (%v)", err, err)
	}

	if !strings.Contains(sw.Error(), wifiOptionMeshFwding) {
		t.Fatalf("expected error to mention %q, got %q", wifiOptionMeshFwding, sw.Error())
	}
}

// TestBandwidthToHTMode_RoundTripsThroughNetwork pins the wizard's
// bandwidth→htmode table to network.HTModeBandwidthMHz, which the QR
// handler uses in the other direction.
func TestBandwidthToHTMode_RoundTripsThroughNetwork(t *testing.T) {
	for _, mhz := range []uint32{1, 2, 4, 8, 20, 40, 80, 160} {
		htmode := bandwidthToHTMode(mhz)
		got, ok := network.HTModeBandwidthMHz(htmode)
		assert.True(t, ok, "htmode %q for %d MHz must be known to network", htmode, mhz)
		assert.Equal(t, mhz, got, "htmode %q must map back to %d MHz", htmode, mhz)
	}
}
