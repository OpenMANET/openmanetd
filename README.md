# OpenMANETd

Management and operations daemon for OpenMANET mesh networks. Runs on OpenMANET mesh nodes and provides, mesh management, an embedded React web interface, and a gRPC/HTTP API.

## Features

### Web Interface
- Embedded React SPA served from the binary (no external assets required)
- Multi-channel PTT grid with waveform visualization and mic level monitoring
- Real-time receive audio display and optional replay buffer
- Mesh topology map and neighbor status
- WiFi, network, and DHCP configuration pages
- System dashboard with quick actions (reboot, restart services)
- On-device speech-to-text transcription via Whisper (model downloaded on demand)

### Talk Groups
- Exclusive talk group selection via `SelectTalkGroup` — only one talk group sends/receives at a time
- Selectable from the RPC/web UI or the Raven's onboard 5-position hardware GPIO selector (`comms.gpioSelector.enable` to disable the hardware path)
- Selection changes are announced through the local speaker with a short per-channel voice clip
- Real-time selection events stream to clients via `StreamTalkGroupEvents`

### Mesh Management (batman-adv / Alfred)
- Publishes node identity, position, gateway, and IP reservation data across the mesh
- Receives the same from neighboring nodes for network-wide discovery
- Automatic batman-adv gateway detection and advertisement
- Topology snapshot API (`MeshTopologyService`) with 60s churn metrics
  (routes added/lost, gateway changes, reconverge time) for the UI
  topology view

### WiFi & Network Management
- Radio configuration (channel, bandwidth, TX power, mode)
- Connected AP client and mesh peer listing
- Network interface monitoring with live traffic counters
- DHCP server configuration and static lease management
- VXLAN peer management

### Beyond-Line-of-Sight (BLOS)
- Extends mesh range via Tailscale/Headscale VPN
- Automatically creates VXLAN tunnels to Tailscale peers
- Configurable auth key and custom login server (Headscale support)
- Live overlay-status API: tunnel identity, RX/TX counters with 60s rates,
  DERP region, per-peer table, ACL tags and advertised routes
- Server-streaming event RPC emits peer add/lost, online/offline, backend
  state, and DERP changes for real-time UI dashboards

### GNSS / GPS
- Integrates with `gpsd` to stream position data
- Publishes in NMEA and/or Cursor-on-Target (CoT) formats

### API
- Protobuf-defined services exposed via [Connect RPC](https://connectrpc.com/) (gRPC + HTTP/JSON)
- Services: `CommsService`, `DashboardService`, `WifiConfigService`, `NetworkInterfaceService`, `BLOSService`, `MeshTopologyService`, `GNSSService`, node and status endpoints
- Server-streaming RPCs for audio (`CommsService`) and BLOS events (`BLOSService`)
- WebSocket endpoint for real-time audio streaming
- Session-based authentication (PAM-backed); works via cookie for the Web UI or `Authorization: Bearer` header for direct callers — see [docs/api-authentication.md](docs/api-authentication.md)

## Architecture

```
cmd/                              CLI entry points (root daemon, frontend-only mode)
internal/openmanet/               Top-level wiring — starts all subsystems
internal/openmanet/server/        ConnectRPC HTTP server + service registration
internal/openmanet/server/handlers/   Service handler implementations
internal/api/                     AUTO-GENERATED protobuf + ConnectRPC Go stubs
internal/comms/                   Opus/RTP audio, PTT control, jitter buffer
internal/blos/                    BLOS/Tailscale/VXLAN overlay
internal/mgmt/                    Alfred mesh management, wireless config
internal/gpsd/                    gpsd integration, NMEA/CoT publishing
internal/network/                 Network interface, DHCP, routing, UCI config
internal/frontend/                Embedded HTTP server (serves SPA, proxies /api and /ws)
internal/database/                SQLite: schema.sql + query.sql; models/ is generated
internal/config/                  Viper-backed config (/etc/openmanetd/config.yml)
proto/                            Git submodule — .proto source files
frontend/                         React SPA (Vite); build output → static/
```

**Ports:** API + WebSocket = **8087**. Frontend = **8081** (proxies `/api/*` and `/ws` to 8087).

## Quickstart

Start the DevContainer and run:

```bash
make build          # fmt → vet → buf → sqlc-gen → frontend → compile
./bin/openmanetd
```

Use `make build-lite` for a smaller binary (~5 MB) without audio/comms hardware dependencies.

**Frontend dev server** (hot-reload, pointed at a remote node):
```bash
cd frontend && VITE_API_TARGET=http://<node-ip>:8081 pnpm run dev
```

**Frontend-only mode** (no local database or hardware):
```bash
./bin/openmanetd frontend --api-address http://<node-ip>:8087
```

## Configuration

The config file lives at `/etc/openmanetd/config.yml`. Key sections:

| Section | Purpose |
|---------|---------|
| `alfred.*` | Batman-adv alfred socket, data type publish toggles |
| `blos.*` | Tailscale enable, auth key, status poll interval, advertised mesh subnet |
| `gnss.*` | gpsd enable, NMEA/CoT output |
| `meshTopology.*` | Delta-tracker sample interval and ring size (powers the 60s churn panel) |
| `frontend.*` | HTTP/HTTPS listen addresses, TLS cert paths |
| `api.*` | gRPC API listen address |
| `database.*` | SQLite file path |
| `instrumentation.*` | Periodic JSON snapshot capture (see `docs/instrumentation-snapshot.md`) |
| `runtime.*` | Memory limit, GC tuning |

## Developer Tools

- A **DevContainer** is included with all required native libraries (CGO, PortAudio, SQLite, etc.).
- The protobuf specs live in `proto/`, a git submodule from [OpenMANET/protobuf](https://github.com/OpenMANET/protobufs). If empty: `git submodule update --init --recursive`, then `make buf`.
- **CGO is required** for the full build (`go-sqlite3`, `portaudio`, `go-alfred`). Use `make build-lite` to skip comms/whisper.
- Never edit `internal/api/`, `frontend/src/gen/`, or `internal/database/models/` — these are generated and will be overwritten.

| Task | Command |
|------|---------|
| Full build | `make build` |
| Lite build (no hardware deps) | `make build-lite` |
| Run Go tests | `make test` |
| Integration tests | `make integration-test` |
| Frontend tests | `make test-frontend` |
| Lint (Go + React) | `make lint` |
| Regenerate protobuf | `make buf` |
| Regenerate database models | `make sqlc-gen` |

Further documentation can be found in the [Getting Started](docs/GETTING_STARTED.md) doc.
