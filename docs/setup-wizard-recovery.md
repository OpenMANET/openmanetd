# Setup wizard recovery

If a device runs the first-boot setup wizard but ends up unreachable
afterward — a network reload failed, the new SSID/IP never came up, or the
wizard was completed with the wrong uplink — you can re-open the wizard from a
console, serial, or recovery shell with:

```sh
openmanetd setup-reset
```

**Use this only over console/serial/recovery.** Running it on a working device
disables session auth and re-opens the wizard, and the next wizard run resets
the device's UCI state from scratch.

## What it changes

`setup-reset` flips two flags in `/etc/openmanetd/config.yml` and one UCI flag:

| Flag | New value | Why |
|------|-----------|-----|
| `setup.complete` | `false` | The wizard's precondition guard admits the run again. |
| `auth.enable` | `false` | The wizard is reachable without a login session. |
| `luci.wizard.used` | `0` | Both the LuCI mesh wizard and the Go wizard set this to `1` on completion and refuse to re-run while it is set. |

All three must be cleared together. The Go wizard's re-apply guard refuses a run
when *either* `setup.complete` is true *or* `luci.wizard.used` is `1`, so clearing
only `setup.complete` would leave the wizard unreachable. `setup-reset` clears
all three in one command.

## After running it

Restart the daemon so it re-reads the configuration, then reconnect to the
wizard URL:

```sh
/etc/init.d/openmanetd restart
```

## What happens on the next wizard run

The wizard renumbers the device off its throwaway `10.41.254.x` bootstrap
address to a mesh-unique address on the daemon's first reservation tick (about
125 seconds after `bat0` comes up), then reboots the device. It comes back on
its final mesh address; `<hostname>.local` keeps working across the move. This
self-reboot is expected — the review screen and success panel say so.

## Related

- [Setup wizard overview](setup-wizard.md) — phases, the UCI each writes, the
  two-stage addressing, and the config keys the wizard and daemon touch.
