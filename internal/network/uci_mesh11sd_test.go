package network

import (
	"errors"
	"testing"

	"github.com/digineo/go-uci/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMesh11sdMock returns a mockConfigReader pre-populated with the
// mesh11sd sections that exist on a fresh OpenMANET reference device.
func newMesh11sdMock(t *testing.T) *mockConfigReader {
	t.Helper()

	m := &mockConfigReader{
		data:         map[string]map[string]map[string][]string{},
		sectionTypes: map[string]map[string]string{},
		anonSections: map[string][]string{},
	}

	require.NoError(t, m.AddSection("mesh11sd", "setup", "mesh11sd"))
	require.NoError(t, m.AddSection("mesh11sd", "mesh_params", "mesh11sd"))
	require.NoError(t, m.SetType("mesh11sd", "setup", "enabled", uci.TypeOption, "0"))
	require.NoError(t, m.SetType("mesh11sd", "mesh_params", "mesh_fwding", uci.TypeOption, "1"))
	require.NoError(t, m.SetType("mesh11sd", "mesh_params", "mesh_gate_announcements", uci.TypeOption, "0"))
	require.NoError(t, m.SetType("mesh11sd", "mesh_params", "mesh_nolearn", uci.TypeOption, "0"))

	m.commitCalled = false
	m.commitCount = 0

	return m
}

func TestGetMesh11sdMeshParamsWithReader(t *testing.T) {
	m := newMesh11sdMock(t)

	got, err := GetMesh11sdMeshParamsWithReader(m)
	require.NoError(t, err)

	assert.Equal(t, "0", got.MeshGateAnnouncements)
	assert.Equal(t, "1", got.MeshFwding)
	assert.Equal(t, "0", got.MeshNolearn)
}

func TestSetMeshGateAnnouncements_FlipsToOne(t *testing.T) {
	m := newMesh11sdMock(t)

	require.NoError(t, SetMeshGateAnnouncements(m, "1"))

	v, ok := m.Get("mesh11sd", "mesh_params", "mesh_gate_announcements")
	require.True(t, ok)
	require.Len(t, v, 1)
	assert.Equal(t, "1", v[0])

	// Helper does not commit; the wizard handler batches.
	assert.Equal(t, 0, m.commitCount)
}

func TestSetMeshGateAnnouncements_RejectsInvalid(t *testing.T) {
	m := newMesh11sdMock(t)

	for _, v := range []string{"", "yes", "2", "true", "01"} {
		err := SetMeshGateAnnouncements(m, v)
		assert.Errorf(t, err, "should reject %q", v)
	}
}

func TestSetMeshFwding_FlipsToZero(t *testing.T) {
	m := newMesh11sdMock(t)

	require.NoError(t, SetMeshFwding(m, "0"))

	v, ok := m.Get("mesh11sd", "mesh_params", "mesh_fwding")
	require.True(t, ok)
	require.Len(t, v, 1)
	assert.Equal(t, "0", v[0])
}

func TestSetMeshFwding_RejectsInvalid(t *testing.T) {
	m := newMesh11sdMock(t)
	assert.Error(t, SetMeshFwding(m, "yes"))
}

func TestSetMeshNolearn_FlipsToOne(t *testing.T) {
	m := newMesh11sdMock(t)

	require.NoError(t, SetMeshNolearn(m, "1"))

	v, ok := m.Get("mesh11sd", "mesh_params", "mesh_nolearn")
	require.True(t, ok)
	require.Len(t, v, 1)
	assert.Equal(t, "1", v[0])

	// Helper does not commit; the wizard handler batches.
	assert.Equal(t, 0, m.commitCount)
}

func TestSetMeshNolearn_RejectsInvalid(t *testing.T) {
	m := newMesh11sdMock(t)

	for _, v := range []string{"", "yes", "2", "true", "01"} {
		err := SetMeshNolearn(m, v)
		assert.Errorf(t, err, "should reject %q", v)
	}
}

func TestSetMesh11sdSetupEnabled_FlipsToOne(t *testing.T) {
	m := newMesh11sdMock(t)

	require.NoError(t, SetMesh11sdSetupEnabled(m, "1"))

	v, ok := m.Get("mesh11sd", "setup", "enabled")
	require.True(t, ok)
	require.Len(t, v, 1)
	assert.Equal(t, "1", v[0])
}

func TestSetMesh11sdSetupEnabled_RejectsInvalid(t *testing.T) {
	m := newMesh11sdMock(t)
	assert.Error(t, SetMesh11sdSetupEnabled(m, "x"))
}

func TestSetMesh11sdHelpers_PropagateSetError(t *testing.T) {
	wantErr := errors.New("set failure")

	for _, tc := range []struct {
		name string
		fn   func(*mockConfigReader) error
	}{
		{"MeshGateAnnouncements", func(m *mockConfigReader) error { return SetMeshGateAnnouncements(m, "1") }},
		{"MeshFwding", func(m *mockConfigReader) error { return SetMeshFwding(m, "0") }},
		{"MeshNolearn", func(m *mockConfigReader) error { return SetMeshNolearn(m, "1") }},
		{"SetupEnabled", func(m *mockConfigReader) error { return SetMesh11sdSetupEnabled(m, "1") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMesh11sdMock(t)
			m.setTypeError = wantErr

			err := tc.fn(m)
			require.Error(t, err)
			assert.ErrorIs(t, err, wantErr)
		})
	}
}

func TestCommitMesh11sd(t *testing.T) {
	m := newMesh11sdMock(t)

	require.NoError(t, CommitMesh11sd(m))
	assert.Equal(t, 1, m.commitCount)
}

func TestCommitMesh11sd_PropagatesError(t *testing.T) {
	m := newMesh11sdMock(t)
	m.commitError = errors.New("disk full")

	err := CommitMesh11sd(m)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "committing mesh11sd")
}
