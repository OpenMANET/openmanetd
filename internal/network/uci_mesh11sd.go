package network

import (
	"fmt"

	"github.com/digineo/go-uci/v2"
)

const (
	mesh11sdConfigName       string = "mesh11sd"
	mesh11sdMeshParamSection string = "mesh_params"
	mesh11sdSetupSection     string = "setup"
)

// UCIMesh11sdMeshParams represents the editable subset of the
// mesh11sd.mesh_params section that the setup wizard touches.
type UCIMesh11sdMeshParams struct {
	// MeshGateAnnouncements is "1" on a mesh gate (the node advertises
	// gateway availability to mesh peers) and "0" on a mesh point.
	MeshGateAnnouncements string `uci:"option mesh_gate_announcements"`
	// MeshFwding is "0" when batman-adv handles forwarding (which is
	// always the case in this codebase, since batman-adv is mandatory)
	// and "1" if the device falls back to in-driver mesh forwarding.
	MeshFwding string `uci:"option mesh_fwding"`
	// MeshNolearn is "1" when batman-adv owns path discovery (always,
	// in this codebase) so the 802.11s driver does not learn mesh paths
	// from received frames; "0" lets the driver learn them.
	MeshNolearn string `uci:"option mesh_nolearn"`
}

// UCIMesh11sdConfigReader wraps go-uci with the same shape as the
// other UCI readers in this package so it composes with the existing
// fakes.
type UCIMesh11sdConfigReader struct {
	tree uci.Tree
}

// NewUCIMesh11sdConfigReader creates a reader rooted at the default
// UCI tree path.
func NewUCIMesh11sdConfigReader() *UCIMesh11sdConfigReader {
	return &UCIMesh11sdConfigReader{tree: uci.NewTree(uci.DefaultTreePath)}
}

func (r *UCIMesh11sdConfigReader) Get(config, section, option string) ([]string, bool) {
	return r.tree.Get(config, section, option)
}

func (r *UCIMesh11sdConfigReader) GetSections(config, secType string) ([]string, error) {
	return r.tree.GetSections(config, secType)
}

func (r *UCIMesh11sdConfigReader) SetType(config, section, option string, typ uci.OptionType, values ...string) error {
	return r.tree.SetType(config, section, option, typ, values...)
}

func (r *UCIMesh11sdConfigReader) Del(config, section, option string) error {
	return r.tree.Del(config, section, option)
}

func (r *UCIMesh11sdConfigReader) AddSection(config, section, typ string) error {
	return r.tree.AddSection(config, section, typ)
}

func (r *UCIMesh11sdConfigReader) DelSection(config, section string) error {
	return r.tree.DelSection(config, section)
}

func (r *UCIMesh11sdConfigReader) Commit() error {
	return r.tree.Commit()
}

func (r *UCIMesh11sdConfigReader) ReloadConfig() error {
	return r.tree.LoadConfig(mesh11sdConfigName, true)
}

// GetMesh11sdMeshParams loads and returns the editable subset of the
// mesh11sd.mesh_params section.
func GetMesh11sdMeshParams() (*UCIMesh11sdMeshParams, error) {
	return GetMesh11sdMeshParamsWithReader(NewUCIMesh11sdConfigReader())
}

// GetMesh11sdMeshParamsWithReader is the testable variant.
func GetMesh11sdMeshParamsWithReader(reader ConfigReader) (*UCIMesh11sdMeshParams, error) {
	cfg := &UCIMesh11sdMeshParams{}

	if v, ok := reader.Get(mesh11sdConfigName, mesh11sdMeshParamSection, "mesh_gate_announcements"); ok && len(v) > 0 {
		cfg.MeshGateAnnouncements = v[0]
	}

	if v, ok := reader.Get(mesh11sdConfigName, mesh11sdMeshParamSection, "mesh_fwding"); ok && len(v) > 0 {
		cfg.MeshFwding = v[0]
	}

	if v, ok := reader.Get(mesh11sdConfigName, mesh11sdMeshParamSection, "mesh_nolearn"); ok && len(v) > 0 {
		cfg.MeshNolearn = v[0]
	}

	return cfg, nil
}

// SetMeshGateAnnouncements writes mesh11sd.mesh_params.mesh_gate_announcements.
// Pass "1" on a mesh gate, "0" on a mesh point. The wizard handler
// derives this from the MeshRole proto enum via the string_name
// annotation. Does not commit; the caller is expected to batch this
// write with other mesh11sd writes and commit once.
func SetMeshGateAnnouncements(reader ConfigReader, value string) error {
	return setMesh11sdMeshParam(reader, "mesh_gate_announcements", value)
}

// SetMeshFwding writes mesh11sd.mesh_params.mesh_fwding. The setup
// wizard always passes "0" because batman-adv is mandatory and
// in-driver forwarding would create routing loops. Does not commit.
func SetMeshFwding(reader ConfigReader, value string) error {
	return setMesh11sdMeshParam(reader, "mesh_fwding", value)
}

// SetMeshNolearn writes mesh11sd.mesh_params.mesh_nolearn. The setup
// wizard always passes "1" because batman-adv owns path discovery; the
// 802.11s driver learning paths from received frames would fight it.
// Does not commit.
func SetMeshNolearn(reader ConfigReader, value string) error {
	return setMesh11sdMeshParam(reader, "mesh_nolearn", value)
}

// SetMesh11sdSetupEnabled writes mesh11sd.setup.enabled. Required for
// byte-equivalence with the captured LuCI after-state fixtures, which
// flip this from "0" to "1" after the wizard runs. Does not commit.
func SetMesh11sdSetupEnabled(reader ConfigReader, value string) error {
	if value != "0" && value != "1" {
		return fmt.Errorf("mesh11sd.setup.enabled must be \"0\" or \"1\", got %q", value)
	}

	if err := reader.SetType(mesh11sdConfigName, mesh11sdSetupSection, "enabled", uci.TypeOption, value); err != nil {
		return fmt.Errorf("setting %s.%s.enabled: %w",
			mesh11sdConfigName, mesh11sdSetupSection, err)
	}

	return nil
}

func setMesh11sdMeshParam(reader ConfigReader, option, value string) error {
	if value != "0" && value != "1" {
		return fmt.Errorf("%s.%s.%s must be \"0\" or \"1\", got %q",
			mesh11sdConfigName, mesh11sdMeshParamSection, option, value)
	}

	if err := reader.SetType(mesh11sdConfigName, mesh11sdMeshParamSection, option, uci.TypeOption, value); err != nil {
		return fmt.Errorf("setting %s.%s.%s: %w",
			mesh11sdConfigName, mesh11sdMeshParamSection, option, err)
	}

	return nil
}

// CommitMesh11sd flushes any pending writes through the reader's tree.
// Provided so the wizard handler can defer committing mesh11sd until
// all mesh11sd writes have been staged.
func CommitMesh11sd(reader ConfigReader) error {
	if err := reader.Commit(); err != nil {
		return fmt.Errorf("committing mesh11sd config: %w", err)
	}

	return nil
}
