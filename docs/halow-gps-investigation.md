# OpenMANET 1.8.1 issues 1–4: investigation and fix plan

Investigated 2026-09-21. Issue 5 is excluded at the user's request. No production behavior changed; probes use temporary files or mocked GPIO. No target hardware was accessed.

## Branch stack

Each repository now has `investigate/halow-band-gps`, created directly from its previously checked-out HEAD:

| Repository | MR target / parent branch | Base commit |
|---|---|---|
| openmanetd | fix/sunkworks-2-5-7 | 010839a34a624caa9886817cad6e53b12361ecab |
| packages | feat/aiscot-halow-scanner | dc866bd143d9502b5201b34eec7183ef7bc86a37 |
| firmware | fix/wm6108-spi-reset | 54f8880ecf437b2118daf37dc3a78bc0bc184b89 |

Existing proto submodule changes and firmware/scripts/tests were present before investigation and are not part of this work. No branches were pushed or MRs opened.

## 1. HaLow binding lost after AP configuration

**Confirmed current-code failure mechanism; exact field-report trigger still needs device capture.**

The current service already prevents explicit AP/STA mode on a Morse radio and preserves batmesh binding on normal mesh edits. However, its ConfigReader is a long-lived go-uci tree (`internal/openmanet/server/server.go`, WifiConfigService construction). The tree caches the entire wireless config. An external wizard or CLI change can update HaLow to batmesh0 while that tree still contains ahwlan.

`stageRadioSettings` in `internal/openmanet/server/handlers/wifi_config.go` calls `SetWirelessDeviceConfigWithReader`, which commits before refreshing the tree. go-uci commits the whole dirty config file, including unrelated cached interfaces. `applyCommitted` reloads only AFTER this destructive commit. Thus even an AP-only channel edit can revert HaLow without explicitly assigning ahwlan to that radio.

Run the safe reproducer from this repository:

```sh
CGO_CFLAGS=-D_GNU_SOURCE go run ./scripts/investigate-wireless-cache
```

Use Go 1.26.3 (the workspace's `/usr/bin/go` is too old). In this environment the cached toolchain is `/home/ubuntu/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.3.linux-amd64/bin/go`.

The probe uses the actual UpdateRadioSettings handler and real go-uci disk persistence, with system reload replaced by a no-op. It primes the cache, commits batmesh0 using a separate tree, then sends an AP-only request:

```
refresh-before-write=false: AP-only RPC leaves HaLow network=ahwlan
refresh-before-write=true: AP-only RPC leaves HaLow network=batmesh0
```

This is a characterization probe: it deliberately expects the existing failure in the first case. Convert it to a regression test expecting preservation when implementing the fix.

**Attribution limit:** current RadioSettings has channel and bandwidth but no writable band field, and the current frontend has no band selector. The probe changes AP channel, not an actual 5g-to-2g UCI band value. A port-8081 session alone does not identify the writer. Capture the running binary version, browser endpoint/payload, UCI before/after, and daemon lifetime relative to the LuCI wizard/manual repair. Repeated failure requires checking when the stale state is recreated. Audit LuCI band-save paths if that was the UI used.

### Proposed daemon MR

1. Refresh affected UCI packages before validation and before the first mutation, under the service write lock. Refresh once at the start of a batch, not between staged updates. Cover wireless AND cached network state when hardifs are involved; the existing ReloadConfig method only refreshes wireless.
2. Keep writes confined to requested device/interface options. Retain Morse mode and hardif guards. Do not run the whole setup wizard as a repair after every AP edit.
3. Add real-file tests: external batmesh0 repair after a cached read; AP-only save; batch path; preservation of unrelated options; refresh failure causes zero writes; existing hardif/network external changes survive.
4. Define shared ownership/serialization across setup, mesh-join and settings writers. A refresh fixes the demonstrated sequential stale-cache case, but does not make external UCI writers atomic with a daemon save. Prefer targeted UCI changes or conflict detection for concurrent writes; avoid claiming a mutex in this handler serializes LuCI.
5. Keep partial-commit/rollback redesign separate unless needed for this fix. Existing batch staging can commit earlier radios before a later failure.

Acceptance: from a freshly configured USB HaLow gate, change AP settings in each supported UI and repeat after a CLI binding repair. wireless HaLow network remains batmesh0, mesh_fwding remains 0, batctl lists wlh0 active after reload, and a three-node chain retains multihop and gateway selection. ESTAB alone is insufficient.

## 2–3. GPS initialization and reset

Confirmed by source and the safe shell probe in packages: `investigations/halow-gps/probe_gps.py`. The USB-path check exits before initialization. The reset pulse fails when pkill is unavailable. Raven has the same reset dependency. Detailed fix/test plan is in packages `investigations/halow-gps/README.md`.

## 4. L76K no-fix / u-blox identification

Still unconfirmed. The local gpsd 3.25 sources support the plausibility of probing: `drivers/drivers.c` sends vendor probes including UBX MON-VER/CFG-PRT; the u-blox driver also has configuration hooks. Both have read-only guards. The package init script currently supplies no read-only option.

A reported driver label does not prove that GPSD caused the missing sentences or lack of fix. Time/leapsecond fields alone do not prove current satellite reception. Inspect raw bytes and debug logs for the actual driver-selection response. Do not disable u-blox support globally based on this report.

Experiment and conditional package MR are described in the packages plan. GPSD documents `-b` as read-only mode preventing receiver configuration: https://gpsd.io/gpsd.html .

## Delivery order

1. Daemon stale-cache fix and regression coverage; GPS board selection and reset ownership fix can proceed independently in packages.
2. Resolve issue 4 with a controlled raw / read-only / normal GPSD experiment after reliable GPS initialization. Add scoped read-only configuration only if justified.
3. Firmware integration MR updates feed/daemon pins to the approved fixes, rebuilds the reported USB image, and validates USB plus SPI/Raven regressions. See firmware `HALOW_GPS_VALIDATION_PLAN.md`.

## Checks completed

- Actual-handler/real-file stale-cache probe and refresh control: both produced the expected contrasting results.
- GPS shell probe: SPI/USB/no-Morse cases and both BCM271x/Raven missing-pkill cases passed.
- Targeted existing handler tests (Success, PartialUpdate, NoModeChange, MeshMode_AlreadyOnBatmesh, RejectsNonMeshModeOnMorse): passed.
- Repository golangci-lint for the new investigation command: zero issues.

Host test commands needed Go 1.26.3 and CGO_CFLAGS=-D_GNU_SOURCE for the PAM dependency. No production changes, full firmware build, electrical checks, or RF/fix acquisition tests were performed.

## Implementation (fix/halow-gps)

Single-radio and batch operations now refresh wireless before any mutation. The production reader also invalidates cached network state, loading it afresh if hardif operations need it. Real-file RPC regression coverage checks that both paths preserve external HaLow binding and vendor options, and batch changes survive. Failed pre-write refresh prevents commits. This fixes the demonstrated sequential stale-cache bug; external writers racing the same save are not serialized by the service mutex.

The investigation command now expects batmesh0 in both scenarios. Issue 3 follows the user's simplified choice: package procps-ng-pkill rather than redesigning GPIO ownership. Packages also adds explicit GPS carrier selection and an optional, default-off GPSD read-only setting for issue 4 diagnosis. Hardware verification remains required.
