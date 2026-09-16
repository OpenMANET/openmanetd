# Setup wizard

The first-boot setup wizard is a 6-step React flow backed by a 15-phase
`ApplySetup` pipeline in `internal/openmanet/server/handlers`. It takes a
`MeshNodeProfile` (identity, mesh radio, uplink, per-radio APs) and stages every
UCI change behind a single atomic commit, so any failure rolls the device back
to its pre-apply state.

This document is the operator- and developer-facing overview. It is deliberately
high level; the phase code and its tests are the authority on exact option
values.

## Reachability

The wizard is only reachable when `setup.enabled=true` and `setup.complete=false`
(and the re-apply guard also refuses once `luci.wizard.used=1`). Shipped firmware
defaults to `setup.enabled=false` and `auth.enable=true`, so the wizard is
unreachable until the packages repo flips those. To re-open the wizard on a
device that already completed it, see
[setup-wizard-recovery.md](setup-wizard-recovery.md).

## What the wizard writes

The wizard snapshots and can roll back these UCI configs:
`wireless`, `network`, `dhcp`, `firewall`, `system`, `mesh11sd`, `umdns`,
`openmanetd`, `luci`.

Highlights that are easy to miss:

- **`mesh11sd`** — the wizard writes `mesh_params.mesh_fwding=0` and
  `mesh_params.mesh_nolearn=1` on every role because batman-adv owns
  forwarding and path discovery; leaving the 802.11s driver to forward or
  learn paths fights batman-adv. `mesh_gate_announcements` follows the role
  (1 on a gate, 0 on a point) and `setup.enabled` is flipped to 1.
- **`luci`** — the wizard marks `luci.wizard.used=1` and deletes
  `luci.main.homepage` when it still points at a first-boot wizard page
  (`admin/morse/landing` or `admin/selectwizard`). Without this, LuCI keeps
  steering operators into the Morse landing flow, which rewrites country,
  channel, timezone and the root password on Apply.
- **`openmanetd`** — the wizard stages `openmanetd.config.dhcpconfigured=0`
  (and `batmesh1configured=0`, or `1` when a mesh backhaul was chosen). These
  are the wizard's half of the two-stage addressing design below.
- **2.4 GHz mesh backhaul tuning** — `MeshBackhaulProfile` carries optional
  `bandwidth_mhz`, `channel` and `country_code`. Zero / empty values keep the
  daemon's fixed defaults (`channel 8`, `htmode HE40`, country untouched);
  otherwise the wizard writes the operator's channel, `HE20`/`HE40` and
  country to the backhaul radio. Channel and width must be set together.
  The link's `wifi-iface` also always carries the daemon's fixed tuning:
  `mcast_rate 24000`, `mesh_nolearn 1`, and `mesh_retry_timeout`,
  `mesh_confirm_timeout`, `mesh_holding_timeout` at `255`
  (`network.SecondaryMeshPolicyOptions`). The daemon adds any of these that
  an older section lacks on its next start. A code scanned on the mesh step
  fills the profile fields (see [mesh-join-qr.md](mesh-join-qr.md)).
- **HaLow radios carry mesh only** — a `wifi-device` with `type 'morse'`
  never gets an AP or STA `wifi-iface`. The wizard rejects a profile that names
  one as an AP or wireless-uplink radio, the reset phase deletes any non-mesh
  iface left on one (older builds wrote a disabled `meshap_<radio>` overlay
  beside the mesh iface), and the settings API refuses to switch such a radio
  to AP/STA.
- **Transport MTU** — the wizard writes `option mtu 1460` on the `br-ahwlan`
  device section and on each ethernet port bridged into it, so the value
  survives a `netifd` reload instead of waiting for the daemon's netlink pass.
  The uplink port and `bat0` are left alone (`bat0` derives its MTU from its
  hardifs; mesh wlan ifaces are named at runtime). LuCI writes no MTU — this is
  a deliberate divergence.

## Two-stage addressing and the self-reboot

The wizard does not assign the device's final mesh address. It writes a
throwaway `10.41.254.x` bootstrap address (a placeholder DHCP pool) and clears
`dhcpconfigured`. After boot, `openmanetd`'s reservation worker listens for peer
gossip over Alfred, claims a mesh-unique address and a 16-lease DHCP window,
sets `dhcpconfigured=1`, and **reboots the device** (about 125 seconds after
`bat0` comes up). The device comes back on its final address; `<hostname>.local`
survives the move. The wizard's review and success screens tell the operator to
expect this reboot.

Re-running the wizard on a device that reserved before is why the wizard stages
`dhcpconfigured=0`: it re-arms the reservation worker so the node stops
advertising its stale bootstrap address.

## Config keys the wizard and daemon share

| Key | Default | Meaning |
|-----|---------|---------|
| `alfred.nodeExpiry` | `24h` | Drop a peer's `mesh_nodes` row (and its reserved address/pool) after this much silence. `0` keeps rows until `resetDBOnStart`. |
| `batman.multicastForceflood` | `true` | `true` writes `bat0.multicast_mode=0` (classic flooding, the shipped state); `false` writes `multicast_mode=1` and turns on batman-adv's IGMP/MLD-snooping optimisations. The daemon reconciles this on every boot. |

## Scenarios

The uplink and device-mode choices classify a run into a scenario that decides
the topology writes: mesh-gate (router on ethernet, router+firewall on
ethernet, or wifi-STA uplink) and mesh-point (extender). `MESH_POINT_MODE_NONE`
is currently hidden in the UI and rejected by validation — the reservation
worker rewrites a non-gateway's `ahwlan` to a static address on its first tick,
so a DHCP-client `ahwlan` would not survive boot.

## Related

- [setup-wizard-recovery.md](setup-wizard-recovery.md) — re-opening the wizard.
- [setup-wizard-bench-checklist.md](setup-wizard-bench-checklist.md) — the
  hardware-only checks CI cannot run.
- [mesh-join-qr.md](mesh-join-qr.md) — sharing and joining a mesh by QR code.
