// Package tzinfo embeds the OpenWrt/LuCI IANA→POSIX timezone table so
// the setup wizard can write both system.zonename (IANA) and
// system.timezone (POSIX) without tzdata on the device. Regenerate
// table.go with scripts/gen-tzinfo when LuCI's tzdata updates.
package tzinfo

import "sort"

// PosixTZ returns the POSIX TZ string for an IANA zone name.
func PosixTZ(name string) (string, bool) {
	v, ok := zones[name]

	return v, ok
}

// Known reports whether name is a valid IANA zone in the table.
func Known(name string) bool {
	_, ok := zones[name]

	return ok
}

// Names returns every IANA zone name, sorted. The slice is freshly
// allocated per call; GetSetupStatus is the only consumer and is not
// a hot path.
func Names() []string {
	out := make([]string, 0, len(zones))
	for k := range zones {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}
