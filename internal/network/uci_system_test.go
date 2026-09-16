package network

import (
	"errors"
	"testing"

	"github.com/digineo/go-uci/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSystemMock returns a mockConfigReader pre-populated with a single
// anonymous system section, mimicking the real /etc/config/system on a
// fresh OpenWrt device.
func newSystemMock(t *testing.T) *mockConfigReader {
	t.Helper()

	m := &mockConfigReader{
		data:         map[string]map[string]map[string][]string{},
		sectionTypes: map[string]map[string]string{},
		anonSections: map[string][]string{},
	}

	require.NoError(t, m.AddSection("system", "", "system"))

	require.NoError(t, m.SetType("system", "@system[0]", "hostname", uci.TypeOption, "BCM2711-97d6"))
	require.NoError(t, m.SetType("system", "@system[0]", "timezone", uci.TypeOption, "UTC"))
	require.NoError(t, m.SetType("system", "@system[0]", "default_wifi_key", uci.TypeOption, "6A6C67MK"))

	// Reset commit counter set by the AddSection/SetType helpers.
	m.commitCalled = false
	m.commitCount = 0

	return m
}

func TestGetSystemConfigWithReader_LoadsAllFields(t *testing.T) {
	m := newSystemMock(t)

	cfg, err := GetSystemConfigWithReader(m)
	require.NoError(t, err)

	assert.Equal(t, "BCM2711-97d6", cfg.Hostname)
	assert.Equal(t, "UTC", cfg.Timezone)
	assert.Equal(t, "6A6C67MK", cfg.DefaultWifiKey)
}

func TestGetSystemConfigWithReader_NoSystemSection(t *testing.T) {
	m := &mockConfigReader{
		data:         map[string]map[string]map[string][]string{},
		sectionTypes: map[string]map[string]string{},
		anonSections: map[string][]string{},
	}

	_, err := GetSystemConfigWithReader(m)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no 'system' section")
}

func TestSetSystemHostnameWithReader_UpdatesAndCommits(t *testing.T) {
	m := newSystemMock(t)

	err := SetSystemHostnameWithReader("openmanet-1", m)
	require.NoError(t, err)

	// Verify the value was written to the anonymous section.
	got, ok := m.Get("system", "@system[0]", "hostname")
	require.True(t, ok)
	require.Len(t, got, 1)
	assert.Equal(t, "openmanet-1", got[0])

	// Verify Commit was called exactly once.
	assert.Equal(t, 1, m.commitCount)
}

func TestSetSystemHostnameWithReader_RejectsEmpty(t *testing.T) {
	m := newSystemMock(t)

	err := SetSystemHostnameWithReader("", m)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be empty")

	// Did not call Commit on validation failure.
	assert.Equal(t, 0, m.commitCount)
}

func TestSetSystemHostnameWithReader_NoSystemSection(t *testing.T) {
	m := &mockConfigReader{
		data:         map[string]map[string]map[string][]string{},
		sectionTypes: map[string]map[string]string{},
		anonSections: map[string][]string{},
	}

	err := SetSystemHostnameWithReader("openmanet-1", m)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no 'system' section")
}

func TestSetSystemHostnameWithReader_PropagatesSetError(t *testing.T) {
	m := newSystemMock(t)
	wantErr := errors.New("set failure")
	m.setTypeError = wantErr

	err := SetSystemHostnameWithReader("openmanet-1", m)
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
	assert.Equal(t, 0, m.commitCount, "no commit on set failure")
}

func TestSetSystemHostnameWithReader_PropagatesCommitError(t *testing.T) {
	m := newSystemMock(t)
	wantErr := errors.New("commit failure")
	m.commitError = wantErr

	err := SetSystemHostnameWithReader("openmanet-1", m)
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestStageSystemTimezoneWithReader(t *testing.T) {
	tests := []struct {
		name     string
		zonename string
		posixTZ  string
		wantErr  string
	}{
		{
			name:     "writes zonename and timezone without committing",
			zonename: "America/Denver",
			posixTZ:  "MST7MDT,M3.2.0,M11.1.0",
		},
		{
			name:    "empty zonename is rejected",
			posixTZ: "MST7MDT,M3.2.0,M11.1.0",
			wantErr: "required",
		},
		{
			name:     "empty posixTZ is rejected",
			zonename: "America/Denver",
			posixTZ:  "",
			wantErr:  "required",
		},
		{
			name:    "both empty is rejected",
			wantErr: "required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newSystemMock(t)

			err := StageSystemTimezoneWithReader(tc.zonename, tc.posixTZ, m)

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				assert.Equal(t, 0, m.commitCount, "no commit on validation failure")

				return
			}

			require.NoError(t, err)

			zone, ok := m.Get("system", "@system[0]", "zonename")
			require.True(t, ok)
			assert.Equal(t, []string{tc.zonename}, zone)

			tzv, ok := m.Get("system", "@system[0]", "timezone")
			require.True(t, ok)
			assert.Equal(t, []string{tc.posixTZ}, tzv)

			assert.Equal(t, 0, m.commitCount, "StageSystemTimezoneWithReader must not commit")
			assert.False(t, m.commitCalled, "StageSystemTimezoneWithReader must not commit")
		})
	}
}

func TestStageSystemTimezoneWithReader_NoSystemSection(t *testing.T) {
	m := &mockConfigReader{
		data:         map[string]map[string]map[string][]string{},
		sectionTypes: map[string]map[string]string{},
		anonSections: map[string][]string{},
	}

	err := StageSystemTimezoneWithReader("America/Denver", "MST7MDT,M3.2.0,M11.1.0", m)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no 'system' section")
}
