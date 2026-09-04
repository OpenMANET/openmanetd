module github.com/openmanet/openmanetd

go 1.26.6

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260825204119-511051f7f437.1
	connectrpc.com/connect v1.20.0
	connectrpc.com/cors v0.1.0
	github.com/common-nighthawk/go-figure v0.0.0-20210622060536-734e95fb86be
	github.com/coreywagehoft/go-tak v1.2.0
	github.com/creack/pty v1.1.24
	github.com/digineo/go-uci/v2 v2.0.0-20231120164223-60c14814b8fe
	github.com/fsnotify/fsnotify v1.10.1
	github.com/gen2brain/alsa v0.6.0
	github.com/gen2brain/malgo v0.11.26
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674
	github.com/gvalkov/golang-evdev v0.0.0-20220815104727-7e27d6ce89b6
	github.com/hraban/opus v0.0.0-20230925203106-0188a62cb302
	github.com/mattn/go-sqlite3 v1.14.50
	github.com/mdlayher/arp v0.0.0-20220512170110-6706a2966875
	github.com/mdlayher/wifi v0.9.0
	github.com/msteinert/pam/v2 v2.1.0
	github.com/openmanet/go-alfred v0.0.0-20240429120015-8f3f3f4e2f4e
	github.com/pion/interceptor v0.1.47
	github.com/pion/rtcp v1.2.17
	github.com/pion/rtp v1.10.5
	github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10
	github.com/rs/cors v1.11.1
	github.com/rs/zerolog v1.35.1
	github.com/spf13/cobra v1.10.2
	github.com/spf13/viper v1.21.0
	github.com/sstallion/go-hid v0.15.0
	github.com/stretchr/testify v1.12.1
	github.com/vishvananda/netlink v1.3.1
	golang.org/x/crypto v0.55.0
	golang.org/x/net v0.58.0
	golang.org/x/sys v0.47.0
	google.golang.org/grpc v1.83.2
	google.golang.org/protobuf v1.36.12
	gopkg.in/yaml.v3 v3.0.1
	tailscale.com v1.102.3
)

require github.com/warthog618/go-gpiocdev v0.9.1

require github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e

require (
	buf.build/go/protovalidate v1.3.0
	cel.dev/expr v0.25.2 // indirect
	connectrpc.com/validate v0.6.0
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/akutz/memconn v0.1.0 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/coder/websocket v1.8.14 // indirect
	github.com/dblohm7/wingoes v0.0.0-20240119213807-a09d6be7affa // indirect
	github.com/fxamacker/cbor/v2 v2.9.0 // indirect
	github.com/go-json-experiment/json v0.0.0-20260214004413-d219187c3433 // indirect
	github.com/go-viper/mapstructure/v2 v2.4.0 // indirect
	github.com/google/cel-go v0.30.0 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hdevalence/ed25519consensus v0.2.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/josharian/native v1.1.0 // indirect
	github.com/jsimonetti/rtnetlink v1.4.1 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.23 // indirect
	github.com/mdlayher/ethernet v0.0.0-20220221185849-529eae5b6118 // indirect
	github.com/mdlayher/genetlink v1.4.0 // indirect
	github.com/mdlayher/netlink v1.11.2 // indirect
	github.com/mdlayher/packet v1.1.2 // indirect
	github.com/mdlayher/socket v0.6.0 // indirect
	github.com/mitchellh/go-ps v1.0.0 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/sagikazarmark/locafero v0.11.0 // indirect
	github.com/sourcegraph/conc v0.3.1-0.20240121214520-5f936abd7ae8 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	github.com/tailscale/go-winio v0.0.0-20231025203758-c4f33415bf55 // indirect
	github.com/vishvananda/netns v0.0.5 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.yaml.in/yaml/v3 v3.0.5
	go4.org/mem v0.0.0-20240501181205-ae6ca9944745
	go4.org/netipx v0.0.0-20231129151722-fdeea329fbba // indirect
	golang.org/x/exp v0.0.0-20260410095643-746e56fc9e2f // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.zx2c4.com/wireguard/windows v0.5.3 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
)

replace github.com/openmanet/go-alfred => ./internal/alfred
