package config

import (
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

// Default configuration values
const (
	DefaultMeshNetInterface   string = "br-ahwlan"
	DefaultDBFile             string = "/etc/openmanetd/openmanetd.db"
	DefaultAlfredMode         string = "primary"
	DefaultAlfredBatInterface string = "bat0"
	// DefaultBatmanMulticastForceflood controls batman-adv's multicast mode
	// through the bat0 `multicast_mode` UCI option, which the kernel defines
	// as the negation of forceflood: true writes multicast_mode=0 and every
	// node classic-floods every multicast frame; false writes
	// multicast_mode=1 and batman-adv's IGMP/MLD-snooping optimisations
	// deliver each group only to nodes that announced membership. The
	// default is true (classic flooding): it is the value the LuCI wizard
	// and both fixture captures leave on the device, and it keeps comms RTP
	// audible without every listener gossiping IGMP/MLD membership across
	// the mesh. Decision D7 (2026-08-27): keep the key, fix the mapping,
	// default true. Operators can set batman.multicastForceflood: false to
	// turn the optimisations on. Mapping: network.MulticastModeForForceflood.
	DefaultBatmanMulticastForceflood   bool   = true
	DefaultAlfredSocketPath            string = "/var/run/alfred.sock"
	DefaultAlfredEnable                bool   = true
	DefaultAlfredDataTypeGateway       bool   = true
	DefaultAlfredDataTypeNode          bool   = true
	DefaultAlfredDataTypePosition      bool   = true
	DefaultAlfredDataTypeAddressReserv bool   = true
	DefaultAlfredDataTypeMeshNeighbors bool   = true
	// DefaultAlfredNodeExpiry is how long a peer may stay silent before its
	// mesh_nodes row is dropped, releasing the address and DHCP window it
	// advertised so the reservation worker stops treating them as taken
	// (ledger D4). Zero disables expiry: rows then live until
	// resetDBOnStart. Key alfred.nodeExpiry, a Go duration string ("24h").
	DefaultAlfredNodeExpiry   time.Duration = 24 * time.Hour
	DefaultCommsEnable        bool          = false
	DefaultCommsProtocol      string        = "rtp"
	DefaultCommsDebug         bool          = false
	DefaultCommsLoopback      bool          = false
	DefaultCommsTrace         bool          = false
	DefaultCommsControlSource string        = "openvlm"
	// DefaultCommsMicGain is the TX digital gain applied after the ADC,
	// in Q8 fixed point with a soft-knee limiter (see comms/audio). 2.0 is
	// the 2026-08-30 bench residual: at the OpenVLM's shipped +20 dB
	// analog gain a loud talker's speech body (~9.5k ADC counts) stays
	// under the 24576 knee, leaving the knee for plosives and transients.
	// The previous 8.0 was compensating for hardware headroom the bench
	// showed does not exist (the ADC clips first) and hard-clipped every
	// sample at or above 4096 counts. Key comms.micGain.
	DefaultCommsMicGain float32 = 2.0
	// DefaultCommsAudioSpeakerVolume is the hardware speaker (DAC) volume
	// percent applied when comms.audio.speakerVolume is unset. 100% maps to
	// the CM108B DAC maximum of 0 dB — the chip has no positive playback
	// gain, so full scale cannot over-drive. A fixed default (rather than
	// the leave-untouched sentinel used for the mic) closes the fleet split
	// where units provisioned with OpenVLM <= 1.0.2 boot at -10 dB from the
	// EEPROM while >= 1.0.3 units boot at 0 dB. Key comms.audio.speakerVolume.
	DefaultCommsAudioSpeakerVolume int = 100
	// DefaultCommsAudioMicVolume is the hardware mic capture (ADC) volume
	// percent applied when comms.audio.micVolume is unset. 100% maps onto
	// the full ALSA range the chip advertises — +23 dB on the CM108B, well
	// above the +8 dB EEPROM boot value — making the capture level
	// deterministic at startup regardless of EEPROM vintage or prior
	// alsamixer state. Operator decision 2026-08-30. Key
	// comms.audio.micVolume.
	DefaultCommsAudioMicVolume                       int    = 100
	DefaultCommsNanoPTTEnable                        bool   = false
	DefaultCommsNanoPTTDevicePath                    string = "/dev/hidraw0/*"
	DefaultCommsNanoPTTDeviceName                    string = ""
	DefaultCommsBluetoothPttEnable                   bool   = false
	DefaultCommsBluetoothPttBluetoothAudioDeviceHint string = ""
	DefaultCommsBluetoothPttBluetoothInputDevice     string = ""
	DefaultCommsBluetoothPttBluetoothOutputDevice    string = ""
	DefaultCommsGPIOSelectorEnable                   bool   = true
	DefaultResetDBOnStart                            bool   = false
	DefaultEnableGNSS                                bool   = false
	DefaultGNSSSendAsNMEA                            bool   = false
	DefaultGNSSSendAsCoT                             bool   = false
	DefaultGNSSCoTUID                                string = ""
	DefaultGNSSSource                                string = "internal"
	DefaultEnableBLOS                                bool   = false
	DefaultBLOSStatusWorkerInterval                  int    = 30 // seconds
	// DefaultMeshTopologyDeltaSampleInterval is how often the mesh
	// topology delta tracker polls batadv-vis for a new snapshot. 5
	// seconds is a compromise between granularity (the UI panel claims
	// a 60-second window) and the cost of forking batadv-vis on every
	// tick.
	DefaultMeshTopologyDeltaSampleInterval int = 5 // seconds
	// DefaultMeshTopologyMaxDeltaSamples caps the rolling snapshot ring
	// at 120 entries, covering 10 minutes of history at the default
	// sample interval. The memory footprint is dominated by the edge
	// set per snapshot; at typical mesh sizes this ring stays well
	// under a megabyte.
	DefaultMeshTopologyMaxDeltaSamples int = 120
	// DefaultBLOSAdvertisedMeshSubnet is the CIDR advertised to the Tailscale
	// control plane via the AdvertiseRoutes preference so remote peers can
	// reach the local mesh through this gateway. Deployments whose mesh
	// subnet differs from 10.41.0.0/16 must override this via
	// blos.advertisedMeshSubnet in the config file. The value must parse as
	// a netip.Prefix; invalid values fall back to this default with a
	// warning at startup.
	DefaultBLOSAdvertisedMeshSubnet     string = "10.41.0.0/16"
	DefaultOpenMANETFrontendHostPort    string = "0.0.0.0:8080"
	DefaultOpenMANETFrontendTLSHostPort string = "0.0.0.0:8081"
	DefaultOpenMANETFrontendTLSCertFile string = ""
	DefaultOpenMANETFrontendTLSKeyFile  string = ""
	DefaultOpenMANETWebsocketPort       int    = 0
	DefaultOpenMANETAPIAddress          string = "0.0.0.0:8087"
	DefaultOpenMANETCommsAPIAddress     string = "http://127.0.0.1:8087"
	DefaultRuntimeMemLimit              string = "64MiB"
	DefaultRuntimeGoGC                  int    = 50
	// DefaultRuntimeGOMAXPROCS of 0 means "auto": defer to the board's
	// ExecutionProfile recommendation, and if the board has none, to Go's
	// runtime default (runtime.NumCPU()).
	DefaultRuntimeGOMAXPROCS      int    = 0
	DefaultDebugPprof             bool   = false
	DefaultDebugPprofAddress      string = "127.0.0.1:6060"
	DefaultCommsEncoderComplexity int    = 5
	// DefaultCommsPacketLossPerc is the Opus encoder's initial
	// packet-loss-percentage hint, controlling how much LBRR (in-band
	// FEC) the encoder allocates bits to. Operators can pin this via
	// comms.packetLossPerc; the FEC adapter is free to raise above
	// the configured floor in response to observed RX loss but will
	// never drop below it. Valid range is [10, 40].
	DefaultCommsPacketLossPerc int = 30
	// CommsPacketLossPercMin is the lower clamp for comms.packetLossPerc.
	// Below 10, LBRR is too small to meaningfully recover a lost frame.
	CommsPacketLossPercMin int = 10
	// CommsPacketLossPercMax is the upper clamp for comms.packetLossPerc.
	// Above 40, primary-frame quality degrades noticeably.
	CommsPacketLossPercMax int = 40
	// DefaultCommsDSCP is the DSCP applied to outgoing RTP/RTCP voice
	// sockets (comms.dscp). 46 (EF, RFC 4594 telephony) maps to skb
	// priority 261 → WMM AC_VI on every mesh hop under Linux's
	// precedence-derived classification. 48 (CS6) maps to priority 262 →
	// AC_VO — flip only after the radio's EDCA behavior is validated
	// on-air. 0 disables marking entirely (today's best-effort behavior).
	DefaultCommsDSCP int = 46
	// CommsDSCPMax is the upper clamp for comms.dscp: DSCP is a 6-bit
	// field, so 63 is the largest encodable value.
	CommsDSCPMax int = 63
	// DefaultCommsPlaybackLatencyMs is the playback-side ALSA period size
	// expressed in milliseconds. computePlaybackPeriodFrames converts it
	// to malgo's DeviceConfig.PeriodSizeInFrames, and the LowLatency
	// profile queues three periods in the playback ring, so worst-case
	// device latency is ~3x this value (USB audio class devices round the
	// period up to the next power of two on top of that). The ring is the
	// audio thread's only protection against OS scheduling stalls — the
	// Go-side jitter buffer cannot help once samples are due at the DAC.
	// The PTT settle wait (transmitSettleWait) also anchors on the modeled
	// ring, so lowering this value shortens PTT start latency too.
	DefaultCommsPlaybackLatencyMs int = 60
	// DefaultCommsCaptureLatencyMs is the capture-side ALSA ring headroom
	// in milliseconds. Unlike playback, the capture period is pinned at
	// one Opus frame (960 frames = 20 ms) so the callback fires once per
	// frame; this value only selects the ring depth in periods via
	// buildCapturePeriods (ceil(ms/20), clamped into [3, 16]). Values of
	// 60 or below all hit the 3-period floor and are equivalent. A
	// preempted capture audio thread silently drops samples (the ADC ring
	// overruns), which remote listeners hear as a gap in the RTP stream —
	// raise this above 60 to buy more headroom on devices that drop.
	DefaultCommsCaptureLatencyMs int = 60
	// DefaultCommsCaptureFramesPerBuffer is the per-callback frame count
	// handed to malgo (DeviceConfig.PeriodSizeInFrames). 0 means
	// audiopool.FrameSize (960 = 20 ms @ 48 kHz), keeping one callback
	// per Opus frame; a negative value lets miniaudio pick a period
	// aligned with the native ALSA period; a positive value is passed
	// through verbatim. The captureChunker re-aligns whatever ALSA
	// actually delivers onto 960-sample (20 ms) Opus frames, so the
	// encoder pipeline never sees the discrepancy.
	DefaultCommsCaptureFramesPerBuffer int  = 0
	DefaultAuthEnable                  bool = true
	// DefaultSetupEnabled is the default value for setup.enabled — the
	// operator-controlled kill switch for the first-boot setup wizard.
	// Defaults to false so factory images ship with the wizard disabled
	// until the feature has been validated in the field. Operators opt in
	// by editing /etc/openmanetd/config.yml.
	DefaultSetupEnabled bool = false
	// DefaultSetupComplete is the default value for setup.complete — the
	// first-boot completion flag flipped to true by the wizard handler at
	// the end of a successful ApplySetup. While false (and setup.enabled
	// is true) the wizard is reachable without authentication; once true
	// the wizard is locked and the daemon refuses ApplySetup with
	// CodeFailedPrecondition.
	DefaultSetupComplete         bool   = false
	DefaultAuthSessionMaxAgeSecs int    = 86400 // 24 hours
	DefaultAuthSessionMaxSize    int    = 16
	DefaultAuthPAMService        string = "login"
	// DefaultInstrumentationEnable controls whether the periodic
	// instrumentation snapshot worker is started at daemon boot.
	DefaultInstrumentationEnable bool = false
	// DefaultInstrumentationIntervalSecs is the capture period used when
	// InstrumentationEnable is true and no override is supplied. 60
	// seconds keeps file churn low while still giving enough resolution
	// for operator triage.
	DefaultInstrumentationIntervalSecs int = 300
	// DefaultInstrumentationSnapshotDir is the filesystem directory new
	// snapshot files are written into.
	DefaultInstrumentationSnapshotDir string = "/tmp"
	DefaultTerminalEnable             bool   = true
	DefaultTerminalShell              string = "/bin/login"
)

// Config holds the application configuration values with automatic reloading support.
type Config struct {
	v                                         *viper.Viper
	OpenMANETFrontendTLSHostPort              string
	CommsNanoPTTDeviceName                    string
	AlfredMode                                string
	AlfredBatInterface                        string
	OpenMANETFrontendTLSCertFile              string
	MeshNetInterface                          string
	CommsNanoPTTDevicePath                    string
	CommsBluetoothPttBluetoothOutputDevice    string
	DBFile                                    string
	CommsControlSource                        string
	CommsProtocol                             string
	CommsBluetoothPttBluetoothInputDevice     string
	CommsBluetoothPttBluetoothAudioDeviceHint string
	OpenMANETFrontendHostPort                 string
	AlfredSocketPath                          string
	OpenMANETFrontendTLSKeyFile               string
	OpenMANETAPIAddress                       string
	OpenMANETCommsAPIAddress                  string
	RuntimeMemLimit                           string
	DebugPprofAddress                         string
	AuthPAMService                            string
	GNSSCoTUID                                string
	GNSSSource                                string
	InstrumentationSnapshotDir                string
	BLOSAdvertisedMeshSubnet                  string
	TerminalShell                             string
	CommsAudioSpeakerControl                  string
	CommsAudioMicControl                      string
	CommsAudioAGCControl                      string
	onChangeCallbacks                         []func(*Config)
	AlfredNodeExpiry                          time.Duration
	BLOSStatusWorkerInterval                  int
	MeshTopologyDeltaSampleInterval           int
	MeshTopologyMaxDeltaSamples               int
	InstrumentationIntervalSecs               int
	OpenMANETWebsocketPort                    int
	CommsEncoderComplexity                    int
	CommsPacketLossPerc                       int
	CommsDSCP                                 int
	CommsPlaybackLatencyMs                    int
	CommsCaptureLatencyMs                     int
	CommsCaptureFramesPerBuffer               int
	CommsAudioSpeakerVolume                   int
	CommsAudioMicVolume                       int
	RuntimeGoGC                               int
	RuntimeGOMAXPROCS                         int
	AuthSessionMaxAgeSecs                     int
	AuthSessionMaxSize                        int
	mu                                        sync.RWMutex
	persistMu                                 sync.Mutex // serializes Persist*Config file I/O
	CommsMicGain                              float32
	AlfredDataTypeAddressReserv               bool
	AlfredDataTypeNode                        bool
	BatmanMulticastForceflood                 bool
	CommsDebug                                bool
	CommsGPIOSelectorEnable                   bool
	CommsEnable                               bool
	CommsTrace                                bool
	CommsNanoPTTEnable                        bool
	CommsBluetoothPttEnable                   bool
	CommsAudioAGC                             bool
	CommsAudioAGCSet                          bool
	ResetDBOnStart                            bool
	EnableGNSS                                bool
	GNSSSendAsNMEA                            bool
	GNSSSendAsCoT                             bool
	DebugPprof                                bool
	AlfredDataTypePosition                    bool
	AlfredEnable                              bool
	AlfredDataTypeGateway                     bool
	AlfredDataTypeMeshNeighbors               bool
	CommsLoopback                             bool
	BLOSEnable                                bool
	AuthEnable                                bool
	SetupEnabled                              bool
	SetupComplete                             bool
	InstrumentationEnable                     bool
	TerminalEnable                            bool
}

// New creates a new Config instance with the given viper instance.
// If v is nil, uses the global viper instance.
// It loads the initial configuration and sets up automatic reloading.
func New(v *viper.Viper) *Config {
	if v == nil {
		v = viper.GetViper()
	}

	c := &Config{
		v:                 v,
		onChangeCallbacks: make([]func(*Config), 0),
	}

	// Load initial configuration
	c.reload()

	// Set up automatic config reloading
	v.WatchConfig()
	v.OnConfigChange(func(e fsnotify.Event) {
		c.reload()
		c.notifyCallbacks()
	})

	return c
}

// NewWithoutWatch creates a new Config instance without starting the file
// watcher. This is useful for tests where fsnotify would cause race conditions.
func NewWithoutWatch(v *viper.Viper) *Config {
	if v == nil {
		v = viper.GetViper()
	}

	c := &Config{
		v:                 v,
		onChangeCallbacks: make([]func(*Config), 0),
	}

	c.reload()

	return c
}

// reload reads all configuration values from viper and updates the Config fields.
func (c *Config) reload() { //nolint:gocognit,gocyclo
	c.mu.Lock()
	defer c.mu.Unlock()

	// Load mesh network configuration
	if val := c.v.GetString("meshNetInterface"); val != "" {
		c.MeshNetInterface = val
	} else {
		c.MeshNetInterface = DefaultMeshNetInterface
	}

	if val := c.v.GetString("dbFile"); val != "" {
		c.DBFile = val
	} else {
		c.DBFile = DefaultDBFile
	}

	if c.v.IsSet("resetDBOnStart") {
		c.ResetDBOnStart = c.v.GetBool("resetDBOnStart")
	} else {
		c.ResetDBOnStart = DefaultResetDBOnStart
	}

	if c.v.IsSet("gnss.enable") {
		c.EnableGNSS = c.v.GetBool("gnss.enable")
	} else {
		c.EnableGNSS = DefaultEnableGNSS
	}

	if c.v.IsSet("gnss.sendAsExternalGNSSSource.sendAsNMEA") {
		c.GNSSSendAsNMEA = c.v.GetBool("gnss.sendAsExternalGNSSSource.sendAsNMEA")
	} else {
		c.GNSSSendAsNMEA = DefaultGNSSSendAsNMEA
	}

	if c.v.IsSet("gnss.sendAsExternalGNSSSource.sendAsCoT") {
		c.GNSSSendAsCoT = c.v.GetBool("gnss.sendAsExternalGNSSSource.sendAsCoT")
	} else {
		c.GNSSSendAsCoT = DefaultGNSSSendAsCoT
	}

	if val := c.v.GetString("gnss.sendAsExternalGNSSSource.cotUID"); val != "" {
		c.GNSSCoTUID = val
	} else {
		c.GNSSCoTUID = DefaultGNSSCoTUID
	}

	if val := c.v.GetString("gnss.source"); val != "" {
		c.GNSSSource = val
	} else {
		c.GNSSSource = DefaultGNSSSource
	}

	if c.v.IsSet("batman.multicastForceflood") {
		c.BatmanMulticastForceflood = c.v.GetBool("batman.multicastForceflood")
	} else {
		c.BatmanMulticastForceflood = DefaultBatmanMulticastForceflood
	}

	// Load Alfred configuration
	if val := c.v.GetString("alfred.mode"); val != "" {
		c.AlfredMode = val
	} else {
		c.AlfredMode = DefaultAlfredMode
	}

	if val := c.v.GetString("alfred.batInterface"); val != "" {
		c.AlfredBatInterface = val
	} else {
		c.AlfredBatInterface = DefaultAlfredBatInterface
	}

	if val := c.v.GetString("alfred.socketPath"); val != "" {
		c.AlfredSocketPath = val
	} else {
		c.AlfredSocketPath = DefaultAlfredSocketPath
	}

	if c.v.IsSet("alfred.enable") {
		c.AlfredEnable = c.v.GetBool("alfred.enable")
	} else {
		c.AlfredEnable = DefaultAlfredEnable
	}

	// Load Alfred data type configuration
	if c.v.IsSet("alfred.dataTypes.gateway") {
		c.AlfredDataTypeGateway = c.v.GetBool("alfred.dataTypes.gateway")
	} else {
		c.AlfredDataTypeGateway = DefaultAlfredDataTypeGateway
	}

	if c.v.IsSet("alfred.dataTypes.node") {
		c.AlfredDataTypeNode = c.v.GetBool("alfred.dataTypes.node")
	} else {
		c.AlfredDataTypeNode = DefaultAlfredDataTypeNode
	}

	if c.v.IsSet("alfred.dataTypes.position") {
		c.AlfredDataTypePosition = c.v.GetBool("alfred.dataTypes.position")
	} else {
		c.AlfredDataTypePosition = DefaultAlfredDataTypePosition
	}

	if c.v.IsSet("alfred.dataTypes.addressReservation") {
		c.AlfredDataTypeAddressReserv = c.v.GetBool("alfred.dataTypes.addressReservation")
	} else {
		c.AlfredDataTypeAddressReserv = DefaultAlfredDataTypeAddressReserv
	}

	if c.v.IsSet("alfred.dataTypes.meshNeighbors") {
		c.AlfredDataTypeMeshNeighbors = c.v.GetBool("alfred.dataTypes.meshNeighbors")
	} else {
		c.AlfredDataTypeMeshNeighbors = DefaultAlfredDataTypeMeshNeighbors
	}

	c.AlfredNodeExpiry = parseDurationOrDefault(c.v.GetString("alfred.nodeExpiry"), DefaultAlfredNodeExpiry)

	// Load comms configuration
	if c.v.IsSet("comms.enable") {
		c.CommsEnable = c.v.GetBool("comms.enable")
	} else {
		c.CommsEnable = DefaultCommsEnable
	}

	if val := strings.ToLower(c.v.GetString("comms.protocol")); val != "" {
		c.CommsProtocol = val
	} else {
		c.CommsProtocol = DefaultCommsProtocol
	}

	if c.v.IsSet("comms.debug") {
		c.CommsDebug = c.v.GetBool("comms.debug")
	} else {
		c.CommsDebug = DefaultCommsDebug
	}

	if c.v.IsSet("comms.gpioSelector.enable") {
		c.CommsGPIOSelectorEnable = c.v.GetBool("comms.gpioSelector.enable")
	} else {
		c.CommsGPIOSelectorEnable = DefaultCommsGPIOSelectorEnable
	}

	if c.v.IsSet("comms.loopback") {
		c.CommsLoopback = c.v.GetBool("comms.loopback")
	} else {
		c.CommsLoopback = DefaultCommsLoopback
	}

	if c.v.IsSet("comms.trace") {
		c.CommsTrace = c.v.GetBool("comms.trace")
	} else {
		c.CommsTrace = DefaultCommsTrace
	}

	if val := strings.ToLower(c.v.GetString("comms.controlSource")); val != "" {
		c.CommsControlSource = val
	} else {
		c.CommsControlSource = DefaultCommsControlSource
	}

	if val := c.v.GetFloat64("comms.micGain"); val > 0 {
		c.CommsMicGain = float32(val)
	} else {
		c.CommsMicGain = DefaultCommsMicGain
	}

	// Load nanoPTT configuration
	if c.v.IsSet("comms.nanoPTT.enable") {
		c.CommsNanoPTTEnable = c.v.GetBool("comms.nanoPTT.enable")
	} else {
		c.CommsNanoPTTEnable = DefaultCommsNanoPTTEnable
	}

	if val := c.v.GetString("comms.nanoPTT.devicePath"); val != "" {
		c.CommsNanoPTTDevicePath = val
	} else {
		c.CommsNanoPTTDevicePath = DefaultCommsNanoPTTDevicePath
	}

	if val := c.v.GetString("comms.nanoPTT.deviceName"); val != "" {
		c.CommsNanoPTTDeviceName = val
	} else {
		c.CommsNanoPTTDeviceName = DefaultCommsNanoPTTDeviceName
	}

	// Load bluetoothPtt configuration
	if c.v.IsSet("comms.bluetoothPtt.enable") {
		c.CommsBluetoothPttEnable = c.v.GetBool("comms.bluetoothPtt.enable")
	} else {
		c.CommsBluetoothPttEnable = DefaultCommsBluetoothPttEnable
	}

	if val := c.v.GetString("comms.bluetoothPtt.BluetoothAudioDeviceHint"); val != "" {
		c.CommsBluetoothPttBluetoothAudioDeviceHint = val
	} else {
		c.CommsBluetoothPttBluetoothAudioDeviceHint = DefaultCommsBluetoothPttBluetoothAudioDeviceHint
	}

	if val := c.v.GetString("comms.bluetoothPtt.BluetoothInputDevice"); val != "" {
		c.CommsBluetoothPttBluetoothInputDevice = val
	} else {
		c.CommsBluetoothPttBluetoothInputDevice = DefaultCommsBluetoothPttBluetoothInputDevice
	}

	if val := c.v.GetString("comms.bluetoothPtt.BluetoothOutputDevice"); val != "" {
		c.CommsBluetoothPttBluetoothOutputDevice = val
	} else {
		c.CommsBluetoothPttBluetoothOutputDevice = DefaultCommsBluetoothPttBluetoothOutputDevice
	}

	// Load BLOS configuration
	if c.v.IsSet("blos.enable") {
		c.BLOSEnable = c.v.GetBool("blos.enable")
	} else {
		c.BLOSEnable = DefaultEnableBLOS
	}

	if val := c.v.GetInt("blos.statusWorkerInterval"); val != 0 {
		c.BLOSStatusWorkerInterval = val
	} else {
		c.BLOSStatusWorkerInterval = DefaultBLOSStatusWorkerInterval
	}

	// Mesh topology delta tracker — polls batadv-vis on an interval and
	// keeps the last N snapshots to compute 60s churn counters.
	if val := c.v.GetInt("meshTopology.deltaSampleInterval"); val > 0 {
		c.MeshTopologyDeltaSampleInterval = val
	} else {
		c.MeshTopologyDeltaSampleInterval = DefaultMeshTopologyDeltaSampleInterval
	}

	if val := c.v.GetInt("meshTopology.maxDeltaSamples"); val > 0 {
		c.MeshTopologyMaxDeltaSamples = val
	} else {
		c.MeshTopologyMaxDeltaSamples = DefaultMeshTopologyMaxDeltaSamples
	}

	// Load the advertised mesh subnet CIDR. Validate that it parses as a
	// netip.Prefix at load time so a malformed value does not propagate to
	// the Tailscale EditPrefs call where it would abort BLOS startup.
	// Invalid values fall back to the default silently here; the BLOS
	// interface setup layer logs the effective value at startup.
	if val := strings.TrimSpace(c.v.GetString("blos.advertisedMeshSubnet")); val != "" {
		if _, parseErr := netip.ParsePrefix(val); parseErr == nil {
			c.BLOSAdvertisedMeshSubnet = val
		} else {
			c.BLOSAdvertisedMeshSubnet = DefaultBLOSAdvertisedMeshSubnet
		}
	} else {
		c.BLOSAdvertisedMeshSubnet = DefaultBLOSAdvertisedMeshSubnet
	}

	if val := c.v.GetString("openmanetFrontendHostPort"); val != "" {
		c.OpenMANETFrontendHostPort = val
	} else {
		c.OpenMANETFrontendHostPort = DefaultOpenMANETFrontendHostPort
	}

	if val := c.v.GetString("frontend.tlsHostPort"); val != "" {
		c.OpenMANETFrontendTLSHostPort = val
	} else {
		c.OpenMANETFrontendTLSHostPort = DefaultOpenMANETFrontendTLSHostPort
	}

	if val := c.v.GetString("frontend.tlsCertFile"); val != "" {
		c.OpenMANETFrontendTLSCertFile = val
	} else {
		c.OpenMANETFrontendTLSCertFile = DefaultOpenMANETFrontendTLSCertFile
	}

	if val := c.v.GetString("frontend.tlsKeyFile"); val != "" {
		c.OpenMANETFrontendTLSKeyFile = val
	} else {
		c.OpenMANETFrontendTLSKeyFile = DefaultOpenMANETFrontendTLSKeyFile
	}

	if val := c.v.GetString("openmanetAPIAddress"); val != "" {
		c.OpenMANETAPIAddress = val
	} else {
		c.OpenMANETAPIAddress = DefaultOpenMANETAPIAddress
	}

	if val := c.v.GetInt("openmanetWebsocketPort"); val != 0 {
		c.OpenMANETWebsocketPort = val
	} else {
		c.OpenMANETWebsocketPort = DefaultOpenMANETWebsocketPort
	}

	if val := c.v.GetString("openmanetCommsAPIAddress"); val != "" {
		c.OpenMANETCommsAPIAddress = val
	} else {
		c.OpenMANETCommsAPIAddress = DefaultOpenMANETCommsAPIAddress
	}

	// Load runtime configuration
	if val := c.v.GetString("runtime.memlimit"); val != "" {
		c.RuntimeMemLimit = val
	} else {
		c.RuntimeMemLimit = DefaultRuntimeMemLimit
	}

	if c.v.IsSet("runtime.gogc") {
		c.RuntimeGoGC = c.v.GetInt("runtime.gogc")
	} else {
		c.RuntimeGoGC = DefaultRuntimeGoGC
	}

	if c.v.IsSet("runtime.gomaxprocs") {
		c.RuntimeGOMAXPROCS = c.v.GetInt("runtime.gomaxprocs")
	} else {
		c.RuntimeGOMAXPROCS = DefaultRuntimeGOMAXPROCS
	}

	// Load debug configuration
	if c.v.IsSet("debug.pprof") {
		c.DebugPprof = c.v.GetBool("debug.pprof")
	} else {
		c.DebugPprof = DefaultDebugPprof
	}

	if val := c.v.GetString("debug.pprofAddress"); val != "" {
		c.DebugPprofAddress = val
	} else {
		c.DebugPprofAddress = DefaultDebugPprofAddress
	}

	// Load comms encoder complexity
	if c.v.IsSet("comms.encoderComplexity") {
		c.CommsEncoderComplexity = c.v.GetInt("comms.encoderComplexity")
	} else {
		c.CommsEncoderComplexity = DefaultCommsEncoderComplexity
	}

	// Load comms packet-loss-perc floor for the Opus encoder / FEC adapter.
	// Clamp to [CommsPacketLossPercMin, CommsPacketLossPercMax]. A value of
	// 0 (or unset) is treated as "use the default". The FEC adapter uses
	// this as its floor and is free to raise above it under observed loss.
	if val := c.v.GetInt("comms.packetLossPerc"); val > 0 {
		switch {
		case val < CommsPacketLossPercMin:
			c.CommsPacketLossPerc = CommsPacketLossPercMin
		case val > CommsPacketLossPercMax:
			c.CommsPacketLossPerc = CommsPacketLossPercMax
		default:
			c.CommsPacketLossPerc = val
		}
	} else {
		c.CommsPacketLossPerc = DefaultCommsPacketLossPerc
	}

	// Load comms DSCP marking for RTP/RTCP voice egress. IsSet-guarded so
	// an explicit `dscp: 0` (marking off) is distinguishable from an
	// absent key (default EF): the zero value is meaningful here, unlike
	// the latency knobs above. Out-of-range values clamp into [0, 63].
	if c.v.IsSet("comms.dscp") {
		switch val := c.v.GetInt("comms.dscp"); {
		case val < 0:
			c.CommsDSCP = 0
		case val > CommsDSCPMax:
			c.CommsDSCP = CommsDSCPMax
		default:
			c.CommsDSCP = val
		}
	} else {
		c.CommsDSCP = DefaultCommsDSCP
	}

	// Load comms playback latency. The value becomes the ALSA period size
	// for every playback device (malgo DeviceConfig.PeriodSizeInFrames
	// after ms→frames conversion); the ring holds three periods, so device
	// latency scales at ~3x this value. Values <= 0 fall back to the
	// default; the requested and effective period plus the modeled ring
	// latency are logged when each playback stream is opened.
	if val := c.v.GetInt("comms.playbackLatencyMs"); val > 0 {
		c.CommsPlaybackLatencyMs = val
	} else {
		c.CommsPlaybackLatencyMs = DefaultCommsPlaybackLatencyMs
	}

	// Load comms capture latency. Sets the capture-side ALSA ring depth:
	// the period is pinned at one Opus frame (960 frames = 20 ms) and this
	// value picks how many periods deep the ring is (ceil(ms/20), clamped
	// into [3, 16]), so values of 60 or below all hit the 3-period floor.
	// The headroom protects the capture audio thread against OS preemption
	// that would otherwise overrun the ADC ring and silently drop samples
	// (heard as stutter by remote listeners). Values <= 0 fall back to the
	// default; the derived period and ring depth are logged when the
	// broadcast stream is opened.
	if val := c.v.GetInt("comms.captureLatencyMs"); val > 0 {
		c.CommsCaptureLatencyMs = val
	} else {
		c.CommsCaptureLatencyMs = DefaultCommsCaptureLatencyMs
	}

	// Load comms capture frames-per-buffer override. This is the per-
	// callback frame count handed to malgo as
	// DeviceConfig.PeriodSizeInFrames. 0 — also the fallback when the key
	// is absent — means audiopool.FrameSize (960 = 20 ms @ 48 kHz) so ALSA
	// wakes the callback once per Opus frame; a negative value lets
	// miniaudio pick a period aligned with the native ALSA period; a
	// positive value is passed through verbatim. The IsSet guard predates
	// the malgo migration (0 and absent now behave identically) and is
	// kept for config-shape stability. The escape hatch is only useful on
	// hardware where the fixed 20 ms period paces badly; see the
	// audio/init.go stream-open log for the requested period and derived
	// ring depth.
	if c.v.IsSet("comms.captureFramesPerBuffer") {
		c.CommsCaptureFramesPerBuffer = c.v.GetInt("comms.captureFramesPerBuffer")
	} else {
		c.CommsCaptureFramesPerBuffer = DefaultCommsCaptureFramesPerBuffer
	}

	// Load comms hardware audio mixer levels. Both volume levels are
	// policy, not passthrough: unset keys apply the 100% defaults so
	// speaker and capture levels do not depend on which OpenVLM EEPROM
	// image a unit was provisioned with or on prior alsamixer state.
	// Out-of-range values are silently clamped to [0, 100].
	c.CommsAudioSpeakerVolume = DefaultCommsAudioSpeakerVolume
	if c.v.IsSet("comms.audio.speakerVolume") {
		c.CommsAudioSpeakerVolume = clampPct(c.v.GetInt("comms.audio.speakerVolume"))
	}

	c.CommsAudioMicVolume = DefaultCommsAudioMicVolume
	if c.v.IsSet("comms.audio.micVolume") {
		c.CommsAudioMicVolume = clampPct(c.v.GetInt("comms.audio.micVolume"))
	}

	c.CommsAudioAGCSet = c.v.IsSet("comms.audio.agc")
	c.CommsAudioAGC = c.v.GetBool("comms.audio.agc")

	c.CommsAudioSpeakerControl = c.v.GetString("comms.audio.speakerControl")
	c.CommsAudioMicControl = c.v.GetString("comms.audio.micControl")
	c.CommsAudioAGCControl = c.v.GetString("comms.audio.agcControl")

	// Load auth configuration
	if c.v.IsSet("auth.enable") {
		c.AuthEnable = c.v.GetBool("auth.enable")
	} else {
		c.AuthEnable = DefaultAuthEnable
	}

	if val := c.v.GetInt("auth.sessionMaxAge"); val > 0 {
		c.AuthSessionMaxAgeSecs = val
	} else {
		c.AuthSessionMaxAgeSecs = DefaultAuthSessionMaxAgeSecs
	}

	if val := c.v.GetInt("auth.sessionMaxSize"); val > 0 {
		c.AuthSessionMaxSize = val
	} else {
		c.AuthSessionMaxSize = DefaultAuthSessionMaxSize
	}

	if val := c.v.GetString("auth.pamService"); val != "" {
		c.AuthPAMService = val
	} else {
		c.AuthPAMService = DefaultAuthPAMService
	}

	// Load setup wizard configuration. setup.enabled is the
	// operator-controlled kill switch and setup.complete is the
	// first-boot completion flag managed by the wizard handler.
	if c.v.IsSet("setup.enabled") {
		c.SetupEnabled = c.v.GetBool("setup.enabled")
	} else {
		c.SetupEnabled = DefaultSetupEnabled
	}

	if c.v.IsSet("setup.complete") {
		c.SetupComplete = c.v.GetBool("setup.complete")
	} else {
		c.SetupComplete = DefaultSetupComplete
	}

	// Load instrumentation snapshot configuration.
	if c.v.IsSet("instrumentation.enable") {
		c.InstrumentationEnable = c.v.GetBool("instrumentation.enable")
	} else {
		c.InstrumentationEnable = DefaultInstrumentationEnable
	}

	if val := c.v.GetInt("instrumentation.intervalSecs"); val > 0 {
		c.InstrumentationIntervalSecs = val
	} else {
		c.InstrumentationIntervalSecs = DefaultInstrumentationIntervalSecs
	}

	if val := c.v.GetString("instrumentation.snapshotDir"); val != "" {
		c.InstrumentationSnapshotDir = val
	} else {
		c.InstrumentationSnapshotDir = DefaultInstrumentationSnapshotDir
	}

	// Load terminal configuration
	if val := c.v.GetString("terminal.shell"); val != "" {
		c.TerminalShell = val
	} else {
		c.TerminalShell = DefaultTerminalShell
	}

	if c.v.IsSet("terminal.enable") {
		c.TerminalEnable = c.v.GetBool("terminal.enable")
	} else {
		c.TerminalEnable = DefaultTerminalEnable
	}
}

// clampPct clamps v into the [0, 100] percent range.
func clampPct(v int) int {
	if v < 0 {
		return 0
	}

	if v > 100 {
		return 100
	}

	return v
}

// OnConfigChange registers a callback function to be called when the configuration changes.
func (c *Config) OnConfigChange(callback func(*Config)) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.onChangeCallbacks = append(c.onChangeCallbacks, callback)
}

// notifyCallbacks calls all registered callback functions.
func (c *Config) notifyCallbacks() {
	c.mu.RLock()
	callbacks := make([]func(*Config), len(c.onChangeCallbacks))
	copy(callbacks, c.onChangeCallbacks)
	c.mu.RUnlock()

	for _, callback := range callbacks {
		callback(c)
	}
}

// GetMeshNetInterface returns the mesh network interface name.
func (c *Config) GetMeshNetInterface() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.MeshNetInterface
}

// GetDBFile returns the database file path.
func (c *Config) GetDBFile() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.DBFile
}

// GetResetDBOnStart returns whether to reset the database on start.
func (c *Config) GetResetDBOnStart() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.ResetDBOnStart
}

// GetBatmanMulticastForceflood returns whether batman-adv multicast forceflood is enabled.
func (c *Config) GetBatmanMulticastForceflood() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.BatmanMulticastForceflood
}

// GetEnableBLOS returns whether BLOS is enabled.
func (c *Config) GetEnableBLOS() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.BLOSEnable
}

// GetAlfredMode returns the Alfred operating mode (primary/secondary).
func (c *Config) GetAlfredMode() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.AlfredMode
}

// GetAlfredBatInterface returns the batman-adv interface name for Alfred.
func (c *Config) GetAlfredBatInterface() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.AlfredBatInterface
}

// GetAlfredSocketPath returns the Alfred socket path.
func (c *Config) GetAlfredSocketPath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.AlfredSocketPath
}

// GetAlfredEnable returns whether Alfred integration is enabled.
func (c *Config) GetAlfredEnable() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.AlfredEnable
}

// GetAlfredDataTypeGateway returns whether gateway data type is enabled.
func (c *Config) GetAlfredDataTypeGateway() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.AlfredDataTypeGateway
}

// GetAlfredDataTypeNode returns whether node data type is enabled.
func (c *Config) GetAlfredDataTypeNode() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.AlfredDataTypeNode
}

// GetAlfredDataTypePosition returns whether position data type is enabled.
func (c *Config) GetAlfredDataTypePosition() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.AlfredDataTypePosition
}

// GetAlfredDataTypeAddressReservation returns whether address reservation data type is enabled.
func (c *Config) GetAlfredDataTypeAddressReservation() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.AlfredDataTypeAddressReserv
}

// GetAlfredDataTypeMeshNeighbors returns whether the mesh-neighbors gossip
// data type is enabled. When true, each node publishes its direct L2
// batman-adv neighbor table (partitioned by RF vs vxlan0) and its own
// best-route originator rows so the serving node can build a true
// mesh-wide topology graph.
func (c *Config) GetAlfredDataTypeMeshNeighbors() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.AlfredDataTypeMeshNeighbors
}

// GetAlfredNodeExpiry returns how long a silent peer stays in mesh_nodes
// before its row (and the address it reserved) is dropped. Zero disables
// expiry.
func (c *Config) GetAlfredNodeExpiry() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.AlfredNodeExpiry
}

// GetCommsEnable returns whether the comms subsystem is enabled.
func (c *Config) GetCommsEnable() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsEnable
}

// GetCommsProtocol returns the comms transport protocol (e.g. rtp).
func (c *Config) GetCommsProtocol() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsProtocol
}

// GetCommsDebug returns whether comms debug mode is enabled.
func (c *Config) GetCommsDebug() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsDebug
}

// GetCommsGPIOSelectorEnable returns whether the hardware talk group
// selector is enabled (honored only on boards that wire one).
func (c *Config) GetCommsGPIOSelectorEnable() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsGPIOSelectorEnable
}

// GetCommsLoopback returns whether comms loopback mode is enabled.
func (c *Config) GetCommsLoopback() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsLoopback
}

// GetCommsTrace returns whether comms trace mode is enabled.
func (c *Config) GetCommsTrace() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsTrace
}

// GetCommsControlSource returns the comms control event source backend.
func (c *Config) GetCommsControlSource() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsControlSource
}

// GetCommsMicGain returns the microphone gain multiplier applied during transmission.
// Values greater than 1.0 amplify; values between 0 and 1.0 attenuate. Zero or negative
// values fall back to 1.0 (unity gain).
func (c *Config) GetCommsMicGain() float32 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsMicGain
}

// GetCommsNanoPTTEnable returns whether the nanoPTT hardware button is enabled.
func (c *Config) GetCommsNanoPTTEnable() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsNanoPTTEnable
}

// GetCommsNanoPTTDevicePath returns the nanoPTT device path glob.
func (c *Config) GetCommsNanoPTTDevicePath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsNanoPTTDevicePath
}

// GetCommsNanoPTTDeviceName returns the nanoPTT device name hint.
func (c *Config) GetCommsNanoPTTDeviceName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsNanoPTTDeviceName
}

// GetCommsBluetoothPttEnable returns whether the Bluetooth PTT source is enabled.
func (c *Config) GetCommsBluetoothPttEnable() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsBluetoothPttEnable
}

// GetCommsBluetoothPttBluetoothAudioDeviceHint returns a shared matcher for selecting both mic and speaker devices.
func (c *Config) GetCommsBluetoothPttBluetoothAudioDeviceHint() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsBluetoothPttBluetoothAudioDeviceHint
}

// GetCommsBluetoothPttBluetoothInputDevice returns the Bluetooth audio input device name or index.
func (c *Config) GetCommsBluetoothPttBluetoothInputDevice() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsBluetoothPttBluetoothInputDevice
}

// GetCommsBluetoothPttBluetoothOutputDevice returns the Bluetooth audio output device name or index.
func (c *Config) GetCommsBluetoothPttBluetoothOutputDevice() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsBluetoothPttBluetoothOutputDevice
}

// GetEnableGNSS returns whether GNSS is enabled.
func (c *Config) GetEnableGNSS() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.EnableGNSS
}

// GetGNSSSendAsNMEA returns whether to send GNSS data as NMEA.
func (c *Config) GetGNSSSendAsNMEA() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.GNSSSendAsNMEA
}

// GetGNSSSendAsCoT returns whether to send GNSS data as CoT.
func (c *Config) GetGNSSSendAsCoT() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.GNSSSendAsCoT
}

// GetGNSSCoTUID returns the CoT UID for GNSS messages.
func (c *Config) GetGNSSCoTUID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.GNSSCoTUID
}

// GetGNSSSource returns which position provider feeds the GNSS subsystem:
// "internal" (local gpsd receiver) or "external_cot" (a connected EUD's
// Cursor-on-Target broadcast).
func (c *Config) GetGNSSSource() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.GNSSSource
}

// BLOSEnabled returns whether BLOS (Beyond Line of Sight) is enabled.
func (c *Config) BLOSEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.BLOSEnable
}

// GetBLOSStatusWorkerInterval returns the BLOS status worker interval in seconds.
func (c *Config) GetBLOSStatusWorkerInterval() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.BLOSStatusWorkerInterval
}

// GetBLOSAdvertisedMeshSubnet returns the CIDR advertised to the Tailscale
// control plane via the AdvertiseRoutes preference. The returned string is
// guaranteed to parse as a netip.Prefix (the config loader validates at load
// time and substitutes DefaultBLOSAdvertisedMeshSubnet when the value is
// missing or malformed).
func (c *Config) GetBLOSAdvertisedMeshSubnet() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.BLOSAdvertisedMeshSubnet
}

// GetMeshTopologyDeltaSampleInterval returns the polling interval in
// seconds used by the mesh-topology delta tracker. Always positive.
func (c *Config) GetMeshTopologyDeltaSampleInterval() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.MeshTopologyDeltaSampleInterval
}

// GetMeshTopologyMaxDeltaSamples returns the cap on the rolling snapshot
// ring used by the mesh-topology delta tracker. Always positive.
func (c *Config) GetMeshTopologyMaxDeltaSamples() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.MeshTopologyMaxDeltaSamples
}

// GetOpenMANETFrontendHostPort returns the OpenMANET frontend host and port.
func (c *Config) GetOpenMANETFrontendHostPort() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.OpenMANETFrontendHostPort
}

// GetOpenMANETFrontendTLSHostPort returns the TLS listen address for the frontend server.
func (c *Config) GetOpenMANETFrontendTLSHostPort() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.OpenMANETFrontendTLSHostPort
}

// GetOpenMANETFrontendTLSCertFile returns the path to the TLS certificate file.
func (c *Config) GetOpenMANETFrontendTLSCertFile() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.OpenMANETFrontendTLSCertFile
}

// GetOpenMANETFrontendTLSKeyFile returns the path to the TLS private key file.
func (c *Config) GetOpenMANETFrontendTLSKeyFile() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.OpenMANETFrontendTLSKeyFile
}

// GetOpenMANETAPIAddress returns the OpenMANET API listen address.
func (c *Config) GetOpenMANETAPIAddress() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.OpenMANETAPIAddress
}

// GetOpenMANETWebsocketPort returns the OpenMANET WebSocket port.
func (c *Config) GetOpenMANETWebsocketPort() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.OpenMANETWebsocketPort
}

// GetOpenMANETCommsAPIAddress returns the OpenMANET comms API address.
func (c *Config) GetOpenMANETCommsAPIAddress() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.OpenMANETCommsAPIAddress
}

// SetOpenMANETAPIAddress overrides the OpenMANET API address.
// This is used by the frontend-only dev mode to point at a remote instance.
func (c *Config) SetOpenMANETAPIAddress(addr string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.OpenMANETAPIAddress = addr
}

// SetOpenMANETWebsocketPort overrides the OpenMANET WebSocket port.
// This is used by the frontend-only dev mode to bind the local frontend server.
func (c *Config) SetOpenMANETWebsocketPort(port int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.OpenMANETWebsocketPort = port
}

// SetOpenMANETFrontendHostPort overrides the OpenMANET frontend host and port.
// This is used by the frontend-only dev mode to bind the local frontend server.
func (c *Config) SetOpenMANETFrontendHostPort(hostPort string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.OpenMANETFrontendHostPort = hostPort
}

// SetOpenMANETCommsAPIAddress overrides the OpenMANET comms API address.
// This is used by the frontend-only dev mode to point at a remote instance.
func (c *Config) SetOpenMANETCommsAPIAddress(addr string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.OpenMANETCommsAPIAddress = addr
}

// GetRuntimeMemLimit returns the runtime memory limit string (e.g. "64MiB").
func (c *Config) GetRuntimeMemLimit() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.RuntimeMemLimit
}

// GetRuntimeGoGC returns the GOGC percentage value.
func (c *Config) GetRuntimeGoGC() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.RuntimeGoGC
}

// GetRuntimeGOMAXPROCS returns the configured maximum number of OS threads
// that execute Go code (the runtime.gomaxprocs config key). A value of 0 means
// "auto": the board's ExecutionProfile recommendation is used, falling back to
// Go's runtime default (runtime.NumCPU()) when the board has no recommendation.
// A value greater than 0 forces that many threads on any board.
func (c *Config) GetRuntimeGOMAXPROCS() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.RuntimeGOMAXPROCS
}

// GetDebugPprof returns whether the pprof debug endpoint is enabled.
func (c *Config) GetDebugPprof() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.DebugPprof
}

// GetDebugPprofAddress returns the pprof listen address.
func (c *Config) GetDebugPprofAddress() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.DebugPprofAddress
}

// GetCommsEncoderComplexity returns the Opus encoder complexity (0-10).
func (c *Config) GetCommsEncoderComplexity() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsEncoderComplexity
}

// GetCommsPacketLossPerc returns the configured Opus packet-loss-perc
// floor for the FEC adapter. The adapter is free to raise above this
// value in response to observed RX loss but will never drop below it.
// Value is clamped to [CommsPacketLossPercMin, CommsPacketLossPercMax]
// by the loader.
func (c *Config) GetCommsPacketLossPerc() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsPacketLossPerc
}

// GetCommsDSCP returns the DSCP for outgoing RTP/RTCP voice sockets,
// clamped to [0, 63] by the loader. 0 means marking is disabled. See
// DefaultCommsDSCP for the value-to-access-class mapping.
func (c *Config) GetCommsDSCP() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsDSCP
}

// GetCommsPlaybackLatencyMs returns the playback ALSA period size in
// milliseconds. malgo queues three periods in the playback ring, so
// worst-case device latency is ~3x this value (USB audio class devices
// round the period up to the next power of two on top of that). The
// playback stream-open log records the requested and effective period
// for verification.
func (c *Config) GetCommsPlaybackLatencyMs() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsPlaybackLatencyMs
}

// GetCommsCaptureLatencyMs returns the capture-side ALSA ring headroom in
// milliseconds. The capture period is fixed at one Opus frame (20 ms);
// this value only selects the ring depth in periods (ceil(ms/20), clamped
// into [3, 16]), so values of 60 or below are equivalent. The broadcast
// stream-open log records the derived period and ring depth.
func (c *Config) GetCommsCaptureLatencyMs() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsCaptureLatencyMs
}

// GetCommsCaptureFramesPerBuffer returns the frame count per capture
// callback handed to malgo. A value of 0 means audiopool.FrameSize
// (960 = 20 ms @ 48 kHz mono), matching the Opus encoder frame so each
// callback produces exactly one RTP packet; a negative value lets
// miniaudio pick a period aligned with the native ALSA period; any
// positive value is passed through verbatim.
func (c *Config) GetCommsCaptureFramesPerBuffer() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsCaptureFramesPerBuffer
}

// GetCommsAudioSpeakerVolume returns the persisted hardware speaker volume
// percent, or DefaultCommsAudioSpeakerVolume (100) when
// comms.audio.speakerVolume is not set.
func (c *Config) GetCommsAudioSpeakerVolume() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsAudioSpeakerVolume
}

// GetCommsAudioMicVolume returns the persisted hardware mic capture volume
// percent, or DefaultCommsAudioMicVolume (100) when comms.audio.micVolume
// is not set.
func (c *Config) GetCommsAudioMicVolume() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsAudioMicVolume
}

// GetCommsAudioAGC returns the persisted Auto Gain Control state and
// whether comms.audio.agc is set at all.
func (c *Config) GetCommsAudioAGC() (enabled, set bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsAudioAGC, c.CommsAudioAGCSet
}

// GetCommsAudioSpeakerControl returns the raw ALSA element-name override
// for the playback volume control, or "" to use the built-in candidates.
func (c *Config) GetCommsAudioSpeakerControl() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsAudioSpeakerControl
}

// GetCommsAudioMicControl returns the raw ALSA element-name override for
// the capture volume control, or "" to use the built-in candidates.
func (c *Config) GetCommsAudioMicControl() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsAudioMicControl
}

// GetCommsAudioAGCControl returns the raw ALSA element-name override for
// the AGC switch, or "" to use the built-in candidates.
func (c *Config) GetCommsAudioAGCControl() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.CommsAudioAGCControl
}

// GetAuthEnable returns whether HTTP authentication is enabled.
func (c *Config) GetAuthEnable() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.AuthEnable
}

// GetAuthSessionMaxAgeSecs returns the session lifetime in seconds.
func (c *Config) GetAuthSessionMaxAgeSecs() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.AuthSessionMaxAgeSecs
}

// GetAuthSessionMaxSize returns the maximum number of concurrent sessions.
func (c *Config) GetAuthSessionMaxSize() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.AuthSessionMaxSize
}

// GetAuthPAMService returns the PAM service name used for authentication.
func (c *Config) GetAuthPAMService() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.AuthPAMService
}

// GetSetupEnabled reports the setup.enabled kill switch. When false, the
// first-boot setup wizard is unreachable regardless of completion state and
// the daemon refuses ApplySetup with CodeUnavailable. Operator-managed.
func (c *Config) GetSetupEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.SetupEnabled
}

// GetSetupComplete reports the setup.complete first-boot flag. When true,
// the wizard is locked and the daemon refuses ApplySetup with
// CodeFailedPrecondition. Wizard-managed; flipped by the handler at the
// end of a successful ApplySetup.
func (c *Config) GetSetupComplete() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.SetupComplete
}

// GetInstrumentationEnable returns whether the periodic instrumentation
// snapshot worker should be started at daemon boot.
func (c *Config) GetInstrumentationEnable() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.InstrumentationEnable
}

// GetInstrumentationIntervalSecs returns the capture period, in seconds,
// used by the instrumentation snapshot worker when enabled.
func (c *Config) GetInstrumentationIntervalSecs() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.InstrumentationIntervalSecs
}

// GetInstrumentationSnapshotDir returns the filesystem directory that
// instrumentation snapshot files are written into.
func (c *Config) GetInstrumentationSnapshotDir() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.InstrumentationSnapshotDir
}

// GetTerminalEnable returns whether the web terminal feature is exposed.
func (c *Config) GetTerminalEnable() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.TerminalEnable
}

// GetTerminalShell returns the absolute path of the shell to spawn for
// terminal sessions.
func (c *Config) GetTerminalShell() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.TerminalShell
}

// parseDurationOrDefault parses a Go duration string, returning def when
// the value is empty, unparsable, or negative. "0" is an explicit,
// accepted zero. The config package has no logger, so a bad value is
// defaulted silently; the config tests pin that.
func parseDurationOrDefault(raw string, def time.Duration) time.Duration {
	if raw == "" {
		return def
	}

	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return def
	}

	return d
}
