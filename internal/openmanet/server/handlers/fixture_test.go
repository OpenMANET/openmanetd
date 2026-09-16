package handlers_test

// fixture_test.go — parse the captured LuCI after-state fixtures under
// testfixtures/setup-wizard/ and compare a staged uciTree section's
// full option set against a fixture section. This is the closest
// achievable form of "fixture equivalence": order-insensitive,
// section-name-tolerant (fixtures use anonymous sections where the
// wizard must use named ones — go-uci AddSection("") limitation),
// but exhaustive over options. Operator-set fields outside wizard
// scope are excluded per call via ignoreOptions.

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixtureSection struct {
	Type    string
	Name    string // "" for anonymous sections
	Options map[string][]string
}

// fixtureRoot walks up from this file to the module root, mirroring
// regdbFixturePath.
func fixtureRoot(t *testing.T) string {
	t.Helper()

	_, here, _, ok := runtime.Caller(0)
	require.True(t, ok)

	root := here
	for range 5 {
		root = filepath.Dir(root)
	}

	return filepath.Join(root, "testfixtures", "setup-wizard")
}

// loadFixture parses testfixtures/setup-wizard/after/<scenario>/<config>.
func loadFixture(t *testing.T, scenario, config string) []fixtureSection {
	t.Helper()

	path := filepath.Join(fixtureRoot(t), "after", scenario, config)
	f, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	var out []fixtureSection

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())

		switch {
		case strings.HasPrefix(line, "config "):
			rest := strings.TrimPrefix(line, "config ")
			fields := strings.SplitN(rest, " ", 2)

			sec := fixtureSection{Type: fields[0], Options: map[string][]string{}}
			if len(fields) == 2 {
				sec.Name = strings.Trim(fields[1], "'")
			}

			out = append(out, sec)
		case strings.HasPrefix(line, "option "), strings.HasPrefix(line, "list "):
			require.NotEmpty(t, out, "option before any config stanza in %s", path)

			rest := strings.TrimPrefix(strings.TrimPrefix(line, "option "), "list ")
			kv := strings.SplitN(rest, " ", 2)
			require.Len(t, kv, 2, "malformed line %q in %s", line, path)
			key := kv[0]
			val := strings.Trim(kv[1], "'")
			last := &out[len(out)-1]
			last.Options[key] = append(last.Options[key], val)
		}
	}

	require.NoError(t, sc.Err())

	return out
}

func findFixtureSection(secs []fixtureSection, secType, name string) *fixtureSection {
	for i := range secs {
		if secs[i].Type != secType {
			continue
		}

		if name == "" && secs[i].Name == "" {
			return &secs[i]
		}

		if secs[i].Name == name {
			return &secs[i]
		}
	}

	return nil
}

func findFixtureSectionByOption(secs []fixtureSection, secType, option, value string) *fixtureSection {
	for i := range secs {
		if secs[i].Type != secType {
			continue
		}

		for _, v := range secs[i].Options[option] {
			if v == value {
				return &secs[i]
			}
		}
	}

	return nil
}

// assertTreeMatchesFixture asserts every fixture option (values and
// list order included) matches the staged tree, minus ignoreOptions.
// It does not detect options the tree carries that the fixture lacks;
// callers that need to prove an option is absent must assert that explicitly.
func assertTreeMatchesFixture(t *testing.T, tr *uciTree, config, treeSection string, fx *fixtureSection, ignoreOptions ...string) {
	t.Helper()

	require.NotNil(t, fx, "fixture section missing")

	ignored := make(map[string]bool, len(ignoreOptions))
	for _, o := range ignoreOptions {
		ignored[o] = true
	}

	for opt, want := range fx.Options {
		if ignored[opt] {
			continue
		}

		got := tr.get(config, treeSection, opt)
		assert.Equal(t, want, got, "%s.%s.%s", config, treeSection, opt)
	}
}

// deepCopyReaderData snapshots a fakeConfigReader's option data as a
// plain nested map so two points in time can be compared with
// assert.Equal without aliasing the reader's live (mutable) storage.
func deepCopyReaderData(r *fakeConfigReader) map[string]map[string]map[string][]string {
	out := make(map[string]map[string]map[string][]string, len(r.data))

	for config, sections := range r.data {
		outSections := make(map[string]map[string][]string, len(sections))

		for section, options := range sections {
			outOptions := make(map[string][]string, len(options))

			for option, values := range options {
				outOptions[option] = append([]string(nil), values...)
			}

			outSections[section] = outOptions
		}

		out[config] = outSections
	}

	return out
}

// ── Ownership map ────────────────────────────────────────────────────────────
//
// The after/ fixtures are not pure wizard output: the openmanetd daemon
// rewrites several rows on first boot (address reservation, gateway
// DNS, batmesh1 conversion) and radio0 on the extender capture was
// hand-edited. A "fixture wins" assertion over those rows would pin
// daemon or operator behavior into the wizard. The table below names
// them; assertTreeMatchesFixtureOwned skips them automatically and the
// wizard's own value for each is pinned in setup_ownership_test.go.
// Source: Wizard Parity Ledger §03 / §07.

// rowOwner names the process that writes a fixture row after (or
// instead of) the wizard.
type rowOwner string

const (
	ownerDaemon rowOwner = "daemon" // openmanetd runtime rewrites it after the wizard
	ownerManual rowOwner = "manual" // hand-edited on the capture device
)

// ownedRow identifies a fixture row (Option set) or a whole section
// (Option "") the wizard does not own. Scenario "" applies to every
// fixture, otherwise only to after/<Scenario>. SectionName "" with
// MatchOption set identifies an anonymous section by one of its
// option values.
type ownedRow struct {
	Scenario    string
	Config      string
	SectionType string
	SectionName string
	MatchOption string
	MatchValue  string
	Option      string
	Owner       rowOwner
}

// ownedRows is the ownership map. A function, not a package var, so
// gochecknoglobals stays quiet in the test package.
func ownedRows() []ownedRow {
	return []ownedRow{
		// AddressReservationWorker replaces the wizard's 10.41.254.x
		// bootstrap address ~125 s after the daemon starts.
		{Config: "network", SectionType: "interface", SectionName: "ahwlan", Option: "ipaddr", Owner: ownerDaemon},
		// GatewayWorker overwrites the wizard's 1.1.1.1 with the elected
		// gateway's address on points. Gates never carry ahwlan.dns
		// (TestCompat_MeshGateAhwlanNoDNS), so the row is point-only.
		{Scenario: "mesh-point-extender", Config: "network", SectionType: "interface", SectionName: "ahwlan", Option: "dns", Owner: ownerDaemon},
		// configureBatmanForceflood reconciles it on every daemon start
		// (commit + reload only when the persisted value differs).
		{Config: "network", SectionType: "interface", SectionName: "bat0", Option: "multicast_mode", Owner: ownerDaemon},
		// The reservation worker rewrites the pool start (offset 100 first).
		{Config: "dhcp", SectionType: "dhcp", SectionName: "ahwlan", Option: "start", Owner: ownerDaemon},
		// Hand-edited 2.4 GHz radio on the extender capture (channel 8,
		// country, cell_density) — excluded from parity.
		{Scenario: "mesh-point-extender", Config: "wireless", SectionType: "wifi-device", SectionName: "radio0", Owner: ownerManual},
		// setupBatMesh1Interface converted the factory AP section into a
		// mesh iface on first boot (ledger F4).
		{Scenario: "mesh-point-extender", Config: "wireless", SectionType: "wifi-iface", SectionName: "default_radio0", Owner: ownerDaemon},
	}
}

// daemonRemovedSections lists sections the wizard writes and the
// daemon deletes after address reservation on mesh points. They are
// absent from the extender fixture, so no parity assertion can cover
// them; setup_ownership_test.go asserts the wizard still writes them.
func daemonRemovedSections() []ownedRow {
	return []ownedRow{
		{Scenario: "mesh-point-extender", Config: "network", SectionType: "interface", SectionName: "lan", Owner: ownerDaemon},
		{Scenario: "mesh-point-extender", Config: "dhcp", SectionType: "dhcp", SectionName: "lan", Owner: ownerDaemon},
	}
}

// matches reports whether r applies to fixture section fx of config in
// scenario.
func (r ownedRow) matches(scenario, config string, fx *fixtureSection) bool {
	if r.Scenario != "" && r.Scenario != scenario {
		return false
	}

	if r.Config != config || r.SectionType != fx.Type {
		return false
	}

	if r.SectionName != "" {
		return r.SectionName == fx.Name
	}

	if r.MatchOption == "" {
		return false
	}

	for _, v := range fx.Options[r.MatchOption] {
		if v == r.MatchValue {
			return true
		}
	}

	return false
}

// sectionOwner reports the owner of a wholly-owned fixture section.
func sectionOwner(scenario, config string, fx *fixtureSection) (rowOwner, bool) {
	for _, r := range ownedRows() {
		if r.Option == "" && r.matches(scenario, config, fx) {
			return r.Owner, true
		}
	}

	return "", false
}

// ownedOptions returns the options of fx that a non-wizard process
// owns. A wholly-owned section yields every option it carries.
func ownedOptions(scenario, config string, fx *fixtureSection) map[string]rowOwner {
	out := make(map[string]rowOwner, 2)

	for _, r := range ownedRows() {
		if !r.matches(scenario, config, fx) {
			continue
		}

		if r.Option == "" {
			for opt := range fx.Options {
				out[opt] = r.Owner
			}

			continue
		}

		out[r.Option] = r.Owner
	}

	return out
}

// assertTreeMatchesFixtureOwned is assertTreeMatchesFixture with the
// ownership map applied: owned options are skipped automatically, and
// a wholly-owned section fails the test outright — its wizard-side
// value belongs in setup_ownership_test.go, not in a parity check.
func assertTreeMatchesFixtureOwned(t *testing.T, tr *uciTree, scenario, config, treeSection string, fx *fixtureSection) {
	t.Helper()

	require.NotNil(t, fx, "fixture section missing")

	if owner, ok := sectionOwner(scenario, config, fx); ok {
		require.Failf(t, "section not wizard-owned",
			"%s %s %q is %s-owned; assert the wizard's value explicitly instead of by fixture parity",
			config, fx.Type, fx.Name, owner)
	}

	owned := ownedOptions(scenario, config, fx)

	ignore := make([]string, 0, len(owned))
	for opt := range owned {
		ignore = append(ignore, opt)
	}

	assertTreeMatchesFixture(t, tr, config, treeSection, fx, ignore...)
}

// extraOptions returns, sorted, the options the staged tree section
// carries that the fixture section lacks, minus allowlist. A pure
// function so the harness can test itself without a fake testing.T.
// A missing tree config or section yields nil.
func extraOptions(tr *uciTree, config, treeSection string, fx *fixtureSection, allowlist ...string) []string {
	allowed := make(map[string]bool, len(allowlist))
	for _, a := range allowlist {
		allowed[a] = true
	}

	var extra []string

	for opt := range tr.reader.data[config][treeSection] {
		if _, inFixture := fx.Options[opt]; inFixture || allowed[opt] {
			continue
		}

		extra = append(extra, opt)
	}

	sort.Strings(extra)

	return extra
}

// assertNoExtraOptions fails when the staged section carries options
// the fixture does not. A failure means the wizard started writing
// something the captured device never had: update the fixture, add
// the row to the ownership map, or allowlist it here with a comment —
// never silently.
func assertNoExtraOptions(t *testing.T, tr *uciTree, config, treeSection string, fx *fixtureSection, allowlist ...string) {
	t.Helper()

	require.NotNil(t, fx, "fixture section missing")

	assert.Empty(t, extraOptions(tr, config, treeSection, fx, allowlist...),
		"%s.%s carries options the %s fixture section lacks", config, treeSection, fx.Name)
}

// newTreeWith builds a one-section uciTree for harness self-tests.
func newTreeWith(config, section string, opts map[string][]string) *uciTree {
	return &uciTree{reader: &fakeConfigReader{
		data:         map[string]map[string]map[string][]string{config: {section: opts}},
		sectionTypes: map[string]map[string]string{config: {section: "test"}},
	}}
}

func TestFixtureParser_GateNetwork(t *testing.T) {
	secs := loadFixture(t, "mesh-gate-router-eth", "network")

	lan := findFixtureSection(secs, "interface", "lan")
	require.NotNil(t, lan)
	assert.Equal(t, []string{"eth0"}, lan.Options["device"])

	bat0 := findFixtureSection(secs, "interface", "bat0")
	require.NotNil(t, bat0)
	assert.Equal(t, []string{"0"}, bat0.Options["multicast_mode"])

	bridge := findFixtureSectionByOption(secs, "device", "name", "br-ahwlan")
	require.NotNil(t, bridge)
	assert.Equal(t, []string{"eth1", "bat0"}, bridge.Options["ports"])
}

// ── Ownership map self-tests ─────────────────────────────────────────────────

func TestOwnership_AhwlanOptionsOwnedByDaemon(t *testing.T) {
	secs := loadFixture(t, "mesh-point-extender", "network")
	fx := findFixtureSection(secs, "interface", "ahwlan")
	require.NotNil(t, fx)

	assert.Equal(t, map[string]rowOwner{"ipaddr": ownerDaemon, "dns": ownerDaemon},
		ownedOptions("mesh-point-extender", "network", fx))

	_, whole := sectionOwner("mesh-point-extender", "network", fx)
	assert.False(t, whole, "ahwlan is wizard-owned apart from two options")
}

func TestOwnership_Bat0OnlyMulticastModeOwned(t *testing.T) {
	secs := loadFixture(t, "mesh-gate-router-eth", "network")
	fx := findFixtureSection(secs, "interface", "bat0")
	require.NotNil(t, fx)

	assert.Equal(t, map[string]rowOwner{"multicast_mode": ownerDaemon},
		ownedOptions("mesh-gate-router-eth", "network", fx))
}

func TestOwnership_Radio0ManualOnExtenderOnly(t *testing.T) {
	ext := loadFixture(t, "mesh-point-extender", "wireless")
	fx := findFixtureSection(ext, "wifi-device", "radio0")
	require.NotNil(t, fx)

	owner, ok := sectionOwner("mesh-point-extender", "wireless", fx)
	require.True(t, ok)
	assert.Equal(t, ownerManual, owner)

	// Every option of a wholly-owned section is owned.
	assert.Len(t, ownedOptions("mesh-point-extender", "wireless", fx), len(fx.Options))

	gate := loadFixture(t, "mesh-gate-router-eth", "wireless")
	gfx := findFixtureSection(gate, "wifi-device", "radio0")
	require.NotNil(t, gfx)

	_, ok = sectionOwner("mesh-gate-router-eth", "wireless", gfx)
	assert.False(t, ok, "gate radio0 was not hand-edited")
}

func TestOwnership_UnownedSectionHasNoOwnedOptions(t *testing.T) {
	secs := loadFixture(t, "mesh-gate-router-eth", "network")
	fx := findFixtureSection(secs, "interface", "batmesh0")
	require.NotNil(t, fx)

	assert.Empty(t, ownedOptions("mesh-gate-router-eth", "network", fx))
}

func TestOwnership_DaemonRemovedSectionsAreAbsentFromExtenderFixture(t *testing.T) {
	for _, row := range daemonRemovedSections() {
		secs := loadFixture(t, row.Scenario, row.Config)
		assert.Nil(t, findFixtureSection(secs, row.SectionType, row.SectionName),
			"%s %s %q must be absent from after/%s (daemon deletes it)", row.Config, row.SectionType, row.SectionName, row.Scenario)
	}
}

// ── Extra-option detection self-tests ───────────────────────────────────────

func TestHarness_ExtraOptions_DetectsOptionFixtureLacks(t *testing.T) {
	tr := newTreeWith("dhcp", "pool", map[string][]string{
		"start": {"100"}, "limit": {"16"}, "ra": {"server"},
	})
	fx := &fixtureSection{Type: "dhcp", Name: "ahwlan", Options: map[string][]string{
		"start": {"100"}, "limit": {"16"},
	}}

	assert.Equal(t, []string{"ra"}, extraOptions(tr, "dhcp", "pool", fx))
}

func TestHarness_ExtraOptions_AllowlistSuppresses(t *testing.T) {
	tr := newTreeWith("dhcp", "pool", map[string][]string{
		"start": {"100"}, "ra": {"server"}, "dns": {"1"},
	})
	fx := &fixtureSection{Type: "dhcp", Name: "ahwlan", Options: map[string][]string{
		"start": {"100"},
	}}

	assert.Equal(t, []string{"ra"}, extraOptions(tr, "dhcp", "pool", fx, "dns"))
	assert.Empty(t, extraOptions(tr, "dhcp", "pool", fx, "dns", "ra"))
}

func TestHarness_ExtraOptions_IdenticalIsEmpty(t *testing.T) {
	tr := newTreeWith("network", "bat0", map[string][]string{"proto": {"batadv"}})
	fx := &fixtureSection{Type: "interface", Name: "bat0", Options: map[string][]string{"proto": {"batadv"}}}

	assert.Empty(t, extraOptions(tr, "network", "bat0", fx))
}

func TestHarness_ExtraOptions_MissingTreeSectionIsEmpty(t *testing.T) {
	tr := newTreeWith("network", "bat0", map[string][]string{"proto": {"batadv"}})
	fx := &fixtureSection{Type: "interface", Name: "ahwlan", Options: map[string][]string{"proto": {"static"}}}

	assert.Empty(t, extraOptions(tr, "network", "ahwlan", fx))
	assert.Empty(t, extraOptions(tr, "firewall", "ahwlan", fx))
}

func TestFixtureParser_ExtenderNetwork(t *testing.T) {
	secs := loadFixture(t, "mesh-point-extender", "network")

	assert.Nil(t, findFixtureSection(secs, "interface", "lan"),
		"the daemon removed lan on the extender capture")

	ahwlan := findFixtureSection(secs, "interface", "ahwlan")
	require.NotNil(t, ahwlan)
	assert.Equal(t, []string{"10.41.1.2"}, ahwlan.Options["ipaddr"])
	assert.Equal(t, []string{"10.41.0.3"}, ahwlan.Options["dns"])

	bridge := findFixtureSectionByOption(secs, "device", "name", "br-ahwlan")
	require.NotNil(t, bridge)
	assert.Contains(t, bridge.Options["ports"], "bat0")
}
