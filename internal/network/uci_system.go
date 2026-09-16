package network

import (
	"fmt"

	"github.com/digineo/go-uci/v2"
)

const systemConfigName string = "system"

// UCISystem represents the editable subset of /etc/config/system that
// the setup wizard touches during the hostname phase. OpenWrt
// conventionally has exactly one anonymous `system` section, addressed
// via `system.@system[0]` in the CLI; we look it up by type rather
// than hardcoding the auto-assigned name so anonymous and named
// sections both work.
type UCISystem struct {
	Hostname       string `uci:"option hostname"`
	Timezone       string `uci:"option timezone"`
	DefaultWifiKey string `uci:"option default_wifi_key"`
	Zonename       string `uci:"option zonename"`
}

// UCISystemConfigReader wraps go-uci with the same shape as the other
// UCI readers in this package so it composes with the existing fakes
// and shared `ConfigReader` interface.
type UCISystemConfigReader struct {
	tree uci.Tree
}

// NewUCISystemConfigReader creates a reader rooted at the default UCI
// tree path.
func NewUCISystemConfigReader() *UCISystemConfigReader {
	return &UCISystemConfigReader{tree: uci.NewTree(uci.DefaultTreePath)}
}

func (r *UCISystemConfigReader) Get(config, section, option string) ([]string, bool) {
	return r.tree.Get(config, section, option)
}

func (r *UCISystemConfigReader) GetSections(config, secType string) ([]string, error) {
	return r.tree.GetSections(config, secType)
}

func (r *UCISystemConfigReader) SetType(config, section, option string, typ uci.OptionType, values ...string) error {
	return r.tree.SetType(config, section, option, typ, values...)
}

func (r *UCISystemConfigReader) Del(config, section, option string) error {
	return r.tree.Del(config, section, option)
}

func (r *UCISystemConfigReader) AddSection(config, section, typ string) error {
	return r.tree.AddSection(config, section, typ)
}

func (r *UCISystemConfigReader) DelSection(config, section string) error {
	return r.tree.DelSection(config, section)
}

func (r *UCISystemConfigReader) Commit() error {
	return r.tree.Commit()
}

func (r *UCISystemConfigReader) ReloadConfig() error {
	return r.tree.LoadConfig(systemConfigName, true)
}

// firstSystemSection returns the section reference for the first
// `system` section in the system UCI config. OpenWrt conventionally
// has exactly one but we look it up to handle both anonymous (`@system[0]`)
// and named cases without per-platform special cases.
func firstSystemSection(reader ConfigReader) (string, error) {
	sections, err := reader.GetSections(systemConfigName, "system")
	if err != nil {
		return "", fmt.Errorf("listing system sections: %w", err)
	}

	if len(sections) == 0 {
		return "", fmt.Errorf("no 'system' section found in UCI system config")
	}

	return sections[0], nil
}

// GetSystemConfig loads and returns the editable subset of the UCI
// system section.
func GetSystemConfig() (*UCISystem, error) {
	return GetSystemConfigWithReader(NewUCISystemConfigReader())
}

// GetSystemConfigWithReader is the testable variant of GetSystemConfig.
func GetSystemConfigWithReader(reader ConfigReader) (*UCISystem, error) {
	section, err := firstSystemSection(reader)
	if err != nil {
		return nil, err
	}

	cfg := &UCISystem{}

	if v, ok := reader.Get(systemConfigName, section, "hostname"); ok && len(v) > 0 {
		cfg.Hostname = v[0]
	}

	if v, ok := reader.Get(systemConfigName, section, "timezone"); ok && len(v) > 0 {
		cfg.Timezone = v[0]
	}

	if v, ok := reader.Get(systemConfigName, section, "default_wifi_key"); ok && len(v) > 0 {
		cfg.DefaultWifiKey = v[0]
	}

	if v, ok := reader.Get(systemConfigName, section, "zonename"); ok && len(v) > 0 {
		cfg.Zonename = v[0]
	}

	return cfg, nil
}

// SetSystemHostname writes the hostname to the first system section
// and commits. Used by the setup wizard's hostname phase.
//
// The caller is responsible for syntax validation; the wizard handler
// validates against RFC 1123 in phase 1 before reaching this point.
func SetSystemHostname(hostname string) error {
	return SetSystemHostnameWithReader(hostname, NewUCISystemConfigReader())
}

// SetSystemHostnameWithReader is the testable variant of
// SetSystemHostname. Writes the hostname AND commits the system tree.
// Use StageSystemHostnameWithReader when you want a staged write that
// commits along with the rest of the wizard's tree in phase 12.
func SetSystemHostnameWithReader(hostname string, reader ConfigReader) error {
	if err := StageSystemHostnameWithReader(hostname, reader); err != nil {
		return err
	}

	if err := reader.Commit(); err != nil {
		return fmt.Errorf("committing system config: %w", err)
	}

	return nil
}

// StageSystemHostnameWithReader writes the hostname to the first
// system section WITHOUT committing. The setup wizard uses this so
// the hostname change is staged in the same in-memory tree as every
// other phase's writes; phase 12's single Commit() then writes them
// all atomically. Splitting into a separate commit (as the original
// SetSystemHostnameWithReader does) opens a window where a failure
// in phases 6-11 would leave the hostname change durable but the
// rest of the wizard's writes rolled back.
func StageSystemHostnameWithReader(hostname string, reader ConfigReader) error {
	if hostname == "" {
		return fmt.Errorf("hostname cannot be empty")
	}

	section, err := firstSystemSection(reader)
	if err != nil {
		return err
	}

	if err := reader.SetType(systemConfigName, section, "hostname", uci.TypeOption, hostname); err != nil {
		return fmt.Errorf("setting %s.%s.hostname: %w", systemConfigName, section, err)
	}

	return nil
}

// StageSystemTimezoneWithReader stages zonename (IANA name) and
// timezone (POSIX TZ string) on the first system section WITHOUT
// committing — same stage-only discipline as
// StageSystemHostnameWithReader; phase 12's single Commit() makes
// the wizard's writes durable atomically.
func StageSystemTimezoneWithReader(zonename, posixTZ string, reader ConfigReader) error {
	if zonename == "" || posixTZ == "" {
		return fmt.Errorf("zonename and posixTZ are required")
	}

	section, err := firstSystemSection(reader)
	if err != nil {
		return err
	}

	if err := reader.SetType(systemConfigName, section, "zonename", uci.TypeOption, zonename); err != nil {
		return fmt.Errorf("setting %s.%s.zonename: %w", systemConfigName, section, err)
	}

	if err := reader.SetType(systemConfigName, section, "timezone", uci.TypeOption, posixTZ); err != nil {
		return fmt.Errorf("setting %s.%s.timezone: %w", systemConfigName, section, err)
	}

	return nil
}
