package network

import (
	"fmt"
	"slices"
	"strings"

	"github.com/digineo/go-uci/v2"
)

const (
	firewallConfigName     string = "firewall"
	firewallZoneType       string = "zone"
	firewallForwardingType string = "forwarding"
	firewallRuleType       string = "rule"

	// FirewallZoneWAN is the conventional OpenWrt name for the upstream
	// (untrusted) firewall zone. The 13 default WAN rules use it as
	// their source zone regardless of which interface is actually the
	// uplink, mirroring the LuCI Morse wizard's behavior — the zone
	// stays around even when its forwarding is disabled.
	FirewallZoneWAN string = "wan"

	// CommsMulticastGroup is the IPv4 multicast group used by the
	// OpenMANET comms RTP traffic. Matches the captured LuCI fixture.
	CommsMulticastGroup string = "239.192.41.1"

	// CommsRTPPortRange is the UDP destination port range used by the
	// OpenMANET comms RTP traffic: the talk-group range owned by
	// internal/config/multicast.go (38801 + channel-1). Matches
	// upstream LuCI's wizard rule and both captured fixtures.
	CommsRTPPortRange string = "38801-38864"

	// BatmanMeshTCPPort is the TCP port used by batman-adv mesh
	// management traffic. Matches the LuCI captured fixture.
	BatmanMeshTCPPort string = "4242"

	// IPv6LinkLocalCIDR is the link-local IPv6 prefix used as the
	// source filter for the Allow-MLD rule.
	IPv6LinkLocalCIDR string = "fe80::/10"

	// Firewall rule field values reused across the default WAN rule set
	// and the firewall_test fixture.
	firewallTargetAccept string = "ACCEPT"
	firewallProtoUDP     string = "udp"
	firewallProtoICMP    string = "icmp"
	firewallFamilyIPv4   string = "ipv4"
	firewallFamilyIPv6   string = "ipv6"
	firewallICMPEchoReq  string = "echo-request"
)

// UCIFirewallZone represents the editable subset of a firewall zone
// section (named or anonymous).
type UCIFirewallZone struct {
	Name    string   `uci:"option name"`
	Input   string   `uci:"option input"`
	Output  string   `uci:"option output"`
	Forward string   `uci:"option forward"`
	MtuFix  string   `uci:"option mtu_fix"`
	Masq    string   `uci:"option masq"`
	Network []string `uci:"list network"`
}

// UCIFirewallForwarding represents the editable subset of a firewall
// forwarding section.
type UCIFirewallForwarding struct {
	Src     string `uci:"option src"`
	Dest    string `uci:"option dest"`
	Enabled string `uci:"option enabled"`
}

// UCIFirewallConfigReader wraps go-uci with the same shape as the
// other UCI readers in this package.
type UCIFirewallConfigReader struct {
	tree uci.Tree
}

// NewUCIFirewallConfigReader creates a reader rooted at the default
// UCI tree path.
func NewUCIFirewallConfigReader() *UCIFirewallConfigReader {
	return &UCIFirewallConfigReader{tree: uci.NewTree(uci.DefaultTreePath)}
}

func (r *UCIFirewallConfigReader) Get(config, section, option string) ([]string, bool) {
	return r.tree.Get(config, section, option)
}

func (r *UCIFirewallConfigReader) GetSections(config, secType string) ([]string, error) {
	return r.tree.GetSections(config, secType)
}

func (r *UCIFirewallConfigReader) SetType(config, section, option string, typ uci.OptionType, values ...string) error {
	return r.tree.SetType(config, section, option, typ, values...)
}

func (r *UCIFirewallConfigReader) Del(config, section, option string) error {
	return r.tree.Del(config, section, option)
}

func (r *UCIFirewallConfigReader) AddSection(config, section, typ string) error {
	return r.tree.AddSection(config, section, typ)
}

func (r *UCIFirewallConfigReader) DelSection(config, section string) error {
	return r.tree.DelSection(config, section)
}

func (r *UCIFirewallConfigReader) Commit() error {
	return r.tree.Commit()
}

func (r *UCIFirewallConfigReader) ReloadConfig() error {
	return r.tree.LoadConfig(firewallConfigName, true)
}

// firewallRule captures the editable subset of a `config rule`
// section. Used internally by AddDefaultWanFirewallRules — the 13
// rules emitted have varying field combinations, so non-empty fields
// are written and empty fields are skipped.
type firewallRule struct {
	Name     string
	Src      string
	Dest     string
	SrcIP    string
	DestIP   string
	Proto    string
	DestPort string
	Target   string
	Family   string
	Limit    string
	IcmpType []string // emitted as a list when len > 0; option when len == 1
}

// defaultWanFirewallRules returns the 13 rules emitted by the LuCI
// Morse wizard for any successful setup, with the local zone name
// substituted into the two block-DHCP rule names. The order matches
// the captured fixture exactly so insertion-order-sensitive
// canonicalization (list options) keeps byte-equivalence.
func defaultWanFirewallRules(localZone string) []firewallRule {
	icmpv6Common := []string{
		firewallICMPEchoReq, "echo-reply", "destination-unreachable",
		"packet-too-big", "time-exceeded", "bad-header",
		"unknown-header-type",
	}
	icmpv6InputExtra := append(append([]string{}, icmpv6Common...),
		"router-solicitation", "neighbour-solicitation",
		"router-advertisement", "neighbour-advertisement",
	)

	return []firewallRule{
		{Name: "Allow-DHCP-Renew", Src: FirewallZoneWAN, Proto: firewallProtoUDP, DestPort: "68", Target: firewallTargetAccept, Family: firewallFamilyIPv4},
		{Name: "Allow-Ping", Src: FirewallZoneWAN, Proto: firewallProtoICMP, IcmpType: []string{firewallICMPEchoReq}, Family: firewallFamilyIPv4, Target: firewallTargetAccept},
		{Name: "Allow-IGMP", Src: FirewallZoneWAN, Proto: "igmp", Family: firewallFamilyIPv4, Target: firewallTargetAccept},
		{Name: "Allow-DHCPv6", Src: FirewallZoneWAN, Proto: firewallProtoUDP, DestPort: "546", Family: firewallFamilyIPv6, Target: firewallTargetAccept},
		{Name: "Allow-MLD", Src: FirewallZoneWAN, Proto: firewallProtoICMP, SrcIP: IPv6LinkLocalCIDR,
			IcmpType: []string{"130/0", "131/0", "132/0", "143/0"},
			Family:   firewallFamilyIPv6, Target: firewallTargetAccept},
		{Name: "Allow-ICMPv6-Input", Src: FirewallZoneWAN, Proto: firewallProtoICMP, IcmpType: icmpv6InputExtra,
			Limit: "1000/sec", Family: firewallFamilyIPv6, Target: firewallTargetAccept},
		{Name: "Allow-ICMPv6-Forward", Src: FirewallZoneWAN, Dest: "*", Proto: firewallProtoICMP, IcmpType: icmpv6Common,
			Limit: "1000/sec", Family: firewallFamilyIPv6, Target: firewallTargetAccept},
		{Name: "Allow-IPSec-ESP", Src: FirewallZoneWAN, Dest: "*", Proto: "esp", Target: firewallTargetAccept},
		{Name: "Allow-ISAKMP", Src: FirewallZoneWAN, Dest: "*", DestPort: "500", Proto: firewallProtoUDP, Target: firewallTargetAccept},
		{Name: "Allow Batman Mesh TCP 4242", Src: "*", Dest: "*", DestPort: BatmanMeshTCPPort, Proto: "tcp", Target: firewallTargetAccept},
		{Name: "Allow Incoming Comms", Src: "*", DestIP: CommsMulticastGroup, Dest: "*", DestPort: CommsRTPPortRange, Proto: firewallProtoUDP, Target: firewallTargetAccept},
		{Name: "Block-DHCP-Request-Out-" + localZone, Src: localZone, Dest: "*", Proto: firewallProtoUDP, DestPort: "67", Target: "DROP", Family: firewallFamilyIPv4},
		{Name: "Block-DHCP-Response-In-" + localZone, Src: "*", Dest: localZone, Proto: firewallProtoUDP, DestPort: "68", Target: "DROP", Family: firewallFamilyIPv4},
	}
}

// wizardRuleSectionName converts a rule's display Name to a UCI-safe
// section identifier. UCI section names accept letters, digits, and
// underscore; everything else is collapsed to a single underscore. A
// `wizard_rule_` prefix and the prior-section index disambiguate from
// any user-named rules while keeping the set self-describing on disk.
func wizardRuleSectionName(displayName string, priorIndex int) string {
	var b strings.Builder

	b.Grow(len(displayName) + 16)
	b.WriteString("wizard_rule_")

	prevUnderscore := false

	for _, r := range displayName {
		switch {
		case r >= 'a' && r <= 'z',
			r >= '0' && r <= '9':
			b.WriteRune(r)

			prevUnderscore = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))

			prevUnderscore = false
		default:
			if !prevUnderscore {
				b.WriteRune('_')

				prevUnderscore = true
			}
		}
	}

	// Suffix the section count to guarantee uniqueness even when two
	// rules share a (sanitized) display name.
	fmt.Fprintf(&b, "_%d", priorIndex)

	return b.String()
}

// AddDefaultWanFirewallRules emits the 13 default firewall rules into
// the firewall config as anonymous `rule` sections. The first 9 rules
// reference the WAN zone (preserved across all scenarios because the
// `wan` zone always exists in OpenWrt's default firewall config). Two
// wildcard rules accept Batman mesh TCP and Comms multicast UDP. The
// last two block DHCP traffic from leaking onto the local zone.
//
// Caller is responsible for committing.
func AddDefaultWanFirewallRules(reader ConfigReader, localZone string) error {
	if localZone == "" {
		return fmt.Errorf("localZone cannot be empty")
	}

	for _, r := range defaultWanFirewallRules(localZone) {
		if err := addAnonymousRule(reader, r); err != nil {
			return fmt.Errorf("adding rule %q: %w", r.Name, err)
		}
	}

	return nil
}

// addAnonymousRule appends a single `rule` section and writes its
// non-empty fields. The section is named (rather than truly anonymous)
// because the underlying go-uci library's AddSection treats an empty
// name as a lookup against existing anonymous sections, which collides
// with /etc/config/firewall's leading `config defaults` block. A
// derived name from the rule's Name field gives every rule a stable,
// unique section that uci tooling on the device still treats as
// indistinguishable from an anonymous rule for matching/iteration.
func addAnonymousRule(reader ConfigReader, r firewallRule) error {
	priorCount, err := countSections(reader, firewallConfigName, firewallRuleType)
	if err != nil {
		return err
	}

	section := wizardRuleSectionName(r.Name, priorCount)
	if err := reader.AddSection(firewallConfigName, section, firewallRuleType); err != nil {
		return fmt.Errorf("adding rule section: %w", err)
	}

	setOpt := func(option, value string) error {
		if value == "" {
			return nil
		}

		if err := reader.SetType(firewallConfigName, section, option, uci.TypeOption, value); err != nil {
			return fmt.Errorf("set %s.%s.%s: %w", firewallConfigName, section, option, err)
		}

		return nil
	}

	if err := setOpt("name", r.Name); err != nil {
		return err
	}

	if err := setOpt("src", r.Src); err != nil {
		return err
	}

	if err := setOpt("dest", r.Dest); err != nil {
		return err
	}

	if err := setOpt("src_ip", r.SrcIP); err != nil {
		return err
	}

	if err := setOpt("dest_ip", r.DestIP); err != nil {
		return err
	}

	if err := setOpt("proto", r.Proto); err != nil {
		return err
	}

	if err := setOpt("dest_port", r.DestPort); err != nil {
		return err
	}

	switch len(r.IcmpType) {
	case 0:
		// nothing
	case 1:
		if err := setOpt("icmp_type", r.IcmpType[0]); err != nil {
			return err
		}
	default:
		if err := reader.SetType(firewallConfigName, section, "icmp_type", uci.TypeList, r.IcmpType...); err != nil {
			return fmt.Errorf("set %s.%s.icmp_type list: %w", firewallConfigName, section, err)
		}
	}

	if err := setOpt("limit", r.Limit); err != nil {
		return err
	}

	if err := setOpt("target", r.Target); err != nil {
		return err
	}

	if err := setOpt("family", r.Family); err != nil {
		return err
	}

	return nil
}

// countSections returns the number of sections of the given type in
// the named config. Used to derive the @type[N] index of a section
// just added via AddSection with an empty name.
func countSections(reader ConfigReader, config, secType string) (int, error) {
	sections, err := reader.GetSections(config, secType)
	if err != nil {
		return 0, fmt.Errorf("listing %s.%s sections: %w", config, secType, err)
	}

	return len(sections), nil
}

// RemoveAllRules deletes every `rule` section in the firewall config.
// Called by the wizard's reset phase before re-adding the 13 default
// rules so we never accumulate duplicates across re-runs.
func RemoveAllRules(reader ConfigReader) error {
	sections, err := reader.GetSections(firewallConfigName, firewallRuleType)
	if err != nil {
		return fmt.Errorf("listing rule sections: %w", err)
	}

	// Iterate in reverse so deleting earlier sections doesn't shift
	// the @rule[N] indices of later ones we still need to address.
	for i := len(sections) - 1; i >= 0; i-- {
		if err := reader.DelSection(firewallConfigName, sections[i]); err != nil {
			return fmt.Errorf("deleting %s: %w", sections[i], err)
		}
	}

	return nil
}

// WhitelistAndDisableForwardings sets `enabled='0'` on every existing
// forwarding section without deleting them. Mirrors the LuCI wizard's
// strategy: a hard delete would lose any user-named forwardings that
// could be re-enabled by GetOrCreateForwarding, so we just disable.
func WhitelistAndDisableForwardings(reader ConfigReader) error {
	sections, err := reader.GetSections(firewallConfigName, firewallForwardingType)
	if err != nil {
		return fmt.Errorf("listing forwarding sections: %w", err)
	}

	for _, s := range sections {
		if err := reader.SetType(firewallConfigName, s, "enabled", uci.TypeOption, "0"); err != nil {
			return fmt.Errorf("disabling %s: %w", s, err)
		}
	}

	return nil
}

// UnsetMtuFixAndMasq removes the `mtu_fix` and `masq` flags from every
// zone. The wizard re-applies them on the destination zone of any
// new forwarding it creates.
func UnsetMtuFixAndMasq(reader ConfigReader) error {
	sections, err := reader.GetSections(firewallConfigName, firewallZoneType)
	if err != nil {
		return fmt.Errorf("listing zone sections: %w", err)
	}

	for _, s := range sections {
		if err := reader.Del(firewallConfigName, s, "mtu_fix"); err != nil {
			return fmt.Errorf("unsetting mtu_fix on %s: %w", s, err)
		}

		if err := reader.Del(firewallConfigName, s, "masq"); err != nil {
			return fmt.Errorf("unsetting masq on %s: %w", s, err)
		}
	}

	return nil
}

// zoneNameFor returns the human-readable `name` of the zone that
// contains networkID in its `network` list, or empty if no such zone
// exists. Mirrors getZoneForNetwork() in the LuCI uci.js helpers.
func zoneNameFor(reader ConfigReader, networkID string) (string, error) {
	sections, err := reader.GetSections(firewallConfigName, firewallZoneType)
	if err != nil {
		return "", fmt.Errorf("listing zone sections: %w", err)
	}

	for _, s := range sections {
		networks, _ := reader.Get(firewallConfigName, s, "network")
		for _, n := range networks {
			if n == networkID {
				name, _ := reader.Get(firewallConfigName, s, "name")
				if len(name) > 0 {
					return name[0], nil
				}

				return "", nil
			}
		}
	}

	return "", nil
}

// zoneSectionByName returns the section reference of the zone whose
// `name` option equals the supplied name. Returns "" if no such zone
// is found.
func zoneSectionByName(reader ConfigReader, name string) (string, error) {
	sections, err := reader.GetSections(firewallConfigName, firewallZoneType)
	if err != nil {
		return "", fmt.Errorf("listing zone sections: %w", err)
	}

	for _, s := range sections {
		v, _ := reader.Get(firewallConfigName, s, "name")
		if len(v) > 0 && v[0] == name {
			return s, nil
		}
	}

	return "", nil
}

// GetOrCreateZone returns the firewall zone name for the supplied
// network section, creating a new ACCEPT-everything zone if none
// exists. Mirrors getOrCreateZone() in the LuCI Morse wizard's uci.js.
//
// The returned string is the zone's `name` field (used by forwarding
// `src`/`dest`), not the zone's UCI section name. New zones are
// created as named sections with the network ID as the name.
func GetOrCreateZone(reader ConfigReader, networkID string) (string, error) {
	if networkID == "" {
		return "", fmt.Errorf("networkID cannot be empty")
	}

	zoneName, err := zoneNameFor(reader, networkID)
	if err != nil {
		return "", err
	}

	if zoneName != "" {
		return zoneName, nil
	}

	// No existing zone for this network — create one. Resolve a unique
	// section/zone name by appending a counter if `networkID` clashes.
	proposedName := networkID
	for i := 0; ; i++ {
		conflict, err := zoneNameOrSectionExists(reader, proposedName)
		if err != nil {
			return "", err
		}

		if !conflict {
			break
		}

		proposedName = fmt.Sprintf("%s%d", networkID, i+1)
	}

	if err := reader.AddSection(firewallConfigName, proposedName, firewallZoneType); err != nil {
		return "", fmt.Errorf("creating zone section: %w", err)
	}

	if err := reader.SetType(firewallConfigName, proposedName, "name", uci.TypeOption, proposedName); err != nil {
		return "", err
	}

	if err := reader.SetType(firewallConfigName, proposedName, "network", uci.TypeList, networkID); err != nil {
		return "", err
	}

	if err := reader.SetType(firewallConfigName, proposedName, "input", uci.TypeOption, "ACCEPT"); err != nil {
		return "", err
	}

	if err := reader.SetType(firewallConfigName, proposedName, "output", uci.TypeOption, "ACCEPT"); err != nil {
		return "", err
	}

	if err := reader.SetType(firewallConfigName, proposedName, "forward", uci.TypeOption, "ACCEPT"); err != nil {
		return "", err
	}

	return proposedName, nil
}

// zoneNameOrSectionExists reports whether the supplied name is already
// in use as either a UCI section name (any type) or a zone's name
// option, so a new zone created with this name would collide.
func zoneNameOrSectionExists(reader ConfigReader, name string) (bool, error) {
	for _, secType := range []string{firewallZoneType, firewallForwardingType, firewallRuleType} {
		sections, err := reader.GetSections(firewallConfigName, secType)
		if err != nil {
			return false, fmt.Errorf("listing %s sections: %w", secType, err)
		}

		for _, s := range sections {
			if s == name {
				return true, nil
			}

			if secType == firewallZoneType {
				v, _ := reader.Get(firewallConfigName, s, "name")
				if len(v) > 0 && v[0] == name {
					return true, nil
				}
			}
		}
	}

	return false, nil
}

// GetOrCreateForwarding returns the section reference of an enabled
// forwarding from src to dest, creating a new one (named or anonymous
// based on `name`) if no enabled match exists. On creation, `mtu_fix=1`
// is always set on the destination zone; `masq=1` is set on the
// destination zone only when natOnDest is true. Whether a forwarding
// needs source NAT on its destination is per-topology (a mesh gate
// routing onto its upstream needs it, a mesh-point-extender forwarding
// into the mesh does not) rather than a universal LuCI behavior, so
// callers decide explicitly.
//
// If a disabled matching forwarding already exists, it is re-enabled
// rather than duplicated. Forwardings from `src` to other destinations
// are disabled (`enabled='0'`) — the wizard only allows one forwarding
// per source zone.
func GetOrCreateForwarding(reader ConfigReader, src, dest, name string, natOnDest bool) (string, error) {
	if src == "" || dest == "" {
		return "", fmt.Errorf("src and dest are required")
	}

	sections, err := reader.GetSections(firewallConfigName, firewallForwardingType)
	if err != nil {
		return "", fmt.Errorf("listing forwarding sections: %w", err)
	}

	// Already-enabled forwarding for src+dest? Done.
	for _, s := range sections {
		if matchesForwarding(reader, s, src, dest) && forwardingEnabled(reader, s) {
			return s, nil
		}
	}

	if err := setDestZoneNATOptions(reader, dest, natOnDest); err != nil {
		return "", err
	}

	if err := disableOtherForwardingsFromSrc(reader, sections, src); err != nil {
		return "", err
	}

	// Re-enable any disabled forwarding that already matches. Clear
	// the `enabled` option rather than setting it to "1" so a
	// re-enabled forwarding converges to the same shape a freshly
	// created one has — fresh creation below never writes `enabled`
	// at all, and forwardingEnabled treats absence as enabled.
	// Setting an explicit "1" here would leave a stray option a fresh
	// first run never wrote, breaking wizard re-run idempotence.
	for _, s := range sections {
		if matchesForwarding(reader, s, src, dest) {
			if err := reader.Del(firewallConfigName, s, "enabled"); err != nil {
				return "", fmt.Errorf("clearing enabled on %s: %w", s, err)
			}

			return s, nil
		}
	}

	// Create a new forwarding. AddSection with an empty name in
	// digineo/go-uci treats empty as a lookup key, which collides
	// with the firewall config's leading anonymous `defaults` section.
	// Always pass a unique section name; auto-generate one when the
	// caller didn't supply one.
	section := name
	if section == "" {
		section = fmt.Sprintf("wizard_fwd_%d", len(sections))
	}

	if err := reader.AddSection(firewallConfigName, section, firewallForwardingType); err != nil {
		return "", fmt.Errorf("creating forwarding section: %w", err)
	}

	if err := reader.SetType(firewallConfigName, section, "src", uci.TypeOption, src); err != nil {
		return "", err
	}

	if err := reader.SetType(firewallConfigName, section, "dest", uci.TypeOption, dest); err != nil {
		return "", err
	}

	return section, nil
}

// setDestZoneNATOptions sets `mtu_fix=1` on the firewall zone named
// dest, always. `masq=1` is set on the same zone only when natOnDest
// is true — masq is a per-topology decision the caller makes, not an
// automatic consequence of being a forwarding destination. No-op if
// no zone named dest exists.
func setDestZoneNATOptions(reader ConfigReader, dest string, natOnDest bool) error {
	destSection, err := zoneSectionByName(reader, dest)
	if err != nil {
		return err
	}

	if destSection == "" {
		return nil
	}

	if err := reader.SetType(firewallConfigName, destSection, "mtu_fix", uci.TypeOption, "1"); err != nil {
		return err
	}

	if natOnDest {
		if err := reader.SetType(firewallConfigName, destSection, "masq", uci.TypeOption, "1"); err != nil {
			return err
		}
	}

	return nil
}

// disableOtherForwardingsFromSrc disables (`enabled='0'`) every
// forwarding section in sections whose `src` matches src — the wizard
// only allows one active forwarding per source zone.
func disableOtherForwardingsFromSrc(reader ConfigReader, sections []string, src string) error {
	for _, s := range sections {
		v, _ := reader.Get(firewallConfigName, s, "src")
		if len(v) > 0 && v[0] == src {
			if err := reader.SetType(firewallConfigName, s, "enabled", uci.TypeOption, "0"); err != nil {
				return err
			}
		}
	}

	return nil
}

func matchesForwarding(reader ConfigReader, section, src, dest string) bool {
	s, _ := reader.Get(firewallConfigName, section, "src")
	d, _ := reader.Get(firewallConfigName, section, "dest")

	return len(s) > 0 && s[0] == src && len(d) > 0 && d[0] == dest
}

func forwardingEnabled(reader ConfigReader, section string) bool {
	v, ok := reader.Get(firewallConfigName, section, "enabled")
	if !ok {
		return true // default if unset is enabled
	}

	if len(v) == 0 {
		return true
	}

	return v[0] != "0"
}

// SetZoneOption writes `option <key> <value>` on the firewall zone
// whose UCI `name` field equals zoneName. Returns an error if the zone
// doesn't exist (the wizard always calls GetOrCreateZone first, so
// this should never trigger from the wizard's call sites). Used by
// the scenario phase to set mtu_fix=1 and (for gate scenarios) masq=1
// on the local zone, mirroring the LuCI fixture deltas.
//
// Does not commit.
func SetZoneOption(reader ConfigReader, zoneName, key, value string) error {
	if zoneName == "" {
		return fmt.Errorf("zoneName cannot be empty")
	}

	section, err := zoneSectionByName(reader, zoneName)
	if err != nil {
		return err
	}

	if section == "" {
		return fmt.Errorf("firewall zone %q not found", zoneName)
	}

	return reader.SetType(firewallConfigName, section, key, uci.TypeOption, value)
}

// AppendZoneNetwork adds `networkName` to the `network` list of the
// firewall zone whose UCI `name` field equals zoneName. Idempotent:
// if `networkName` is already in the list, no write happens.
//
// Used by the wizard's RouterFirewallEth scenario to ensure `wan6`
// is bound to the `wan` zone; without that, the implicit-REJECT
// default-zone policy drops every IPv6 packet on wan6.
//
// Does not commit.
func AppendZoneNetwork(reader ConfigReader, zoneName, networkName string) error {
	if zoneName == "" {
		return fmt.Errorf("zoneName cannot be empty")
	}

	if networkName == "" {
		return fmt.Errorf("networkName cannot be empty")
	}

	section, err := zoneSectionByName(reader, zoneName)
	if err != nil {
		return err
	}

	if section == "" {
		return fmt.Errorf("firewall zone %q not found", zoneName)
	}

	existing, _ := reader.Get(firewallConfigName, section, "network")
	if slices.Contains(existing, networkName) {
		return nil
	}

	updated := append(append([]string(nil), existing...), networkName)

	return reader.SetType(firewallConfigName, section, "network", uci.TypeList, updated...)
}
