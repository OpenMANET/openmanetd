package network

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/digineo/go-uci/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStageUmdnsNetworksWithReader_TableDriven covers both shapes a
// factory /etc/config/umdns can arrive in: a zero-byte file (no
// `umdns` section at all — the section must be created) and a
// pre-existing section from a prior wizard run (the network list must
// be replaced, not appended to).
func TestStageUmdnsNetworksWithReader_TableDriven(t *testing.T) {
	tests := map[string]struct {
		setup    func(t *testing.T) *mockConfigReader
		networks []string
		wantList []string
	}{
		"empty config creates section": {
			setup: func(t *testing.T) *mockConfigReader {
				t.Helper()

				return &mockConfigReader{
					data:         map[string]map[string]map[string][]string{},
					sectionTypes: map[string]map[string]string{},
					anonSections: map[string][]string{},
				}
			},
			networks: []string{"lan", "ahwlan"},
			wantList: []string{"lan", "ahwlan"},
		},
		"existing section replaces list": {
			setup: func(t *testing.T) *mockConfigReader {
				t.Helper()

				m := &mockConfigReader{
					data:         map[string]map[string]map[string][]string{},
					sectionTypes: map[string]map[string]string{},
					anonSections: map[string][]string{},
				}

				require.NoError(t, m.AddSection("umdns", "wizard_umdns", "umdns"))
				require.NoError(t, m.SetType("umdns", "wizard_umdns", "network", uci.TypeList, "lan"))

				return m
			},
			networks: []string{"lan", "ahwlan"},
			wantList: []string{"lan", "ahwlan"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			m := tc.setup(t)

			err := StageUmdnsNetworksWithReader(m, tc.networks)
			require.NoError(t, err)

			sections, err := m.GetSections("umdns", "umdns")
			require.NoError(t, err)
			require.Len(t, sections, 1)

			got, ok := m.Get("umdns", sections[0], "network")
			require.True(t, ok)
			assert.Equal(t, tc.wantList, got)

			// Staged, not committed — phase 12 owns the atomic commit.
			assert.False(t, m.commitCalled)
		})
	}
}

func TestStageUmdnsNetworksWithReader_RejectsEmptyNetworks(t *testing.T) {
	m := newMockReader()

	err := StageUmdnsNetworksWithReader(m, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "networks are required")
}

// getSectionsErrorReader wraps mockConfigReader and forces GetSections
// to fail on every call, regardless of config/secType. mockConfigReader
// itself never returns errors from GetSections, so this hand-written
// stub is the only way to pin the error-propagation path.
type getSectionsErrorReader struct {
	*mockConfigReader
	getSectionsErr error
}

func (r *getSectionsErrorReader) GetSections(_, _ string) ([]string, error) {
	return nil, r.getSectionsErr
}

// TestStageUmdnsNetworksWithReader_PropagatesGetSectionsError pins that
// a GetSections failure other than os.ErrNotExist (a parse or
// permission error reading /etc/config/umdns) is returned to the caller
// rather than treated as "section absent" and falling into AddSection —
// go-uci's AddSection nil-derefs when the config failed to load for
// any reason but a missing file, and this runs in wizard phase 6. The
// missing-file case is TestStageUmdnsNetworksWithReader_RealTree.
func TestStageUmdnsNetworksWithReader_PropagatesGetSectionsError(t *testing.T) {
	wantErr := errors.New("reading config file failed")
	m := &getSectionsErrorReader{
		mockConfigReader: newMockReader(),
		getSectionsErr:   wantErr,
	}

	err := StageUmdnsNetworksWithReader(m, []string{"lan", "ahwlan"})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)

	// AddSection must never be reached on a GetSections failure.
	assert.Empty(t, m.addSectionCall)
}

// TestStageUmdnsNetworksWithReader_RealTree drives the production
// go-uci path rather than mockConfigReader, because the mock never
// errors from GetSections and so cannot reproduce what a device
// actually does. Two on-disk shapes matter: no /etc/config/umdns at
// all (the umdns package is not installed, or the file was removed)
// and the zero-byte file factory images ship. Both must stage the
// section in memory and, on Commit, materialize a file umdns can read.
func TestStageUmdnsNetworksWithReader_RealTree(t *testing.T) {
	tests := map[string]struct {
		seed func(t *testing.T, dir string)
	}{
		"no config file": {
			seed: func(t *testing.T, _ string) { t.Helper() },
		},
		"zero-byte config file": {
			seed: func(t *testing.T, dir string) {
				t.Helper()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "umdns"), nil, 0o644))
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			tc.seed(t, dir)

			reader := &UCINetworkConfigReader{tree: uci.NewTree(dir)}

			require.NoError(t, StageUmdnsNetworksWithReader(reader, []string{"lan", "ahwlan"}))

			sections, err := reader.GetSections("umdns", "umdns")
			require.NoError(t, err)
			require.Equal(t, []string{"wizard_umdns"}, sections)

			got, ok := reader.Get("umdns", "wizard_umdns", "network")
			require.True(t, ok)
			assert.Equal(t, []string{"lan", "ahwlan"}, got)

			// Phase 12's Commit must write the file so umdns has
			// something to load on reload.
			require.NoError(t, reader.Commit())

			body, err := os.ReadFile(filepath.Join(dir, "umdns"))
			require.NoError(t, err)
			assert.Contains(t, string(body), "config umdns 'wizard_umdns'")
			assert.Contains(t, string(body), "list network 'lan'")
			assert.Contains(t, string(body), "list network 'ahwlan'")
		})
	}
}
