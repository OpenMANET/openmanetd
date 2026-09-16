package config

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
)

// Helper functions for test cases
func boolPtr(b bool) *bool {
	return &b
}

func intPtr(i int) *int {
	return &i
}

func strPtr(s string) *string {
	return &s
}

func float64Ptr(f float64) *float64 {
	return &f
}

func TestGetMeshNetInterface(t *testing.T) {
	tests := []struct {
		name     string
		setValue *string
		want     string
	}{
		{
			name:     "returns configured value",
			setValue: strPtr("wlh0"),
			want:     "wlh0",
		},
		{
			name:     "returns default when empty",
			setValue: strPtr(""),
			want:     DefaultMeshNetInterface,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("meshNetInterface", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetMeshNetInterface()
			if got != tt.want {
				t.Errorf("GetMeshNetInterface() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetAlfredMode(t *testing.T) {
	tests := []struct {
		name     string
		setValue *string
		want     string
	}{
		{
			name:     "returns primary mode",
			setValue: strPtr("primary"),
			want:     "primary",
		},
		{
			name:     "returns secondary mode",
			setValue: strPtr("secondary"),
			want:     "secondary",
		},
		{
			name:     "returns default when empty",
			setValue: strPtr(""),
			want:     DefaultAlfredMode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("alfred.mode", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetAlfredMode()
			if got != tt.want {
				t.Errorf("GetAlfredMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCommsProtocol(t *testing.T) {
	tests := []struct {
		name     string
		setValue *string
		want     string
	}{
		{
			name:     "returns configured protocol",
			setValue: strPtr("rtp"),
			want:     "rtp",
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultCommsProtocol,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("comms.protocol", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetCommsProtocol()
			if got != tt.want {
				t.Errorf("GetCommsProtocol() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCommsEnable(t *testing.T) {
	tests := []struct {
		setValue *bool
		name     string
		want     bool
	}{
		{
			name:     "returns true when enabled",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns false when disabled",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultCommsEnable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("comms.enable", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetCommsEnable()
			if got != tt.want {
				t.Errorf("GetCommsEnable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetAlfredDataTypeGateway(t *testing.T) {
	tests := []struct {
		setValue *bool
		name     string
		want     bool
	}{
		{
			name:     "returns true when enabled",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns false when disabled",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultAlfredDataTypeGateway,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("alfred.dataTypes.gateway", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetAlfredDataTypeGateway()
			if got != tt.want {
				t.Errorf("GetAlfredDataTypeGateway() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetAlfredSocketPath(t *testing.T) {
	tests := []struct {
		name     string
		setValue *string
		want     string
	}{
		{
			name:     "returns configured path",
			setValue: strPtr("/custom/path/alfred.sock"),
			want:     "/custom/path/alfred.sock",
		},
		{
			name:     "returns default when empty",
			setValue: strPtr(""),
			want:     DefaultAlfredSocketPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("alfred.socketPath", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetAlfredSocketPath()
			if got != tt.want {
				t.Errorf("GetAlfredSocketPath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetDBFile(t *testing.T) {
	tests := []struct {
		name     string
		setValue *string
		want     string
	}{
		{
			name:     "returns configured path",
			setValue: strPtr("/custom/db/openmanetd.db"),
			want:     "/custom/db/openmanetd.db",
		},
		{
			name:     "returns default when empty",
			setValue: strPtr(""),
			want:     DefaultDBFile,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("dbFile", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetDBFile()
			if got != tt.want {
				t.Errorf("GetDBFile() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetResetDBOnStart(t *testing.T) {
	tests := []struct {
		setValue *bool
		name     string
		want     bool
	}{
		{
			name:     "returns true when enabled",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns false when disabled",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultResetDBOnStart,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("resetDBOnStart", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetResetDBOnStart()
			if got != tt.want {
				t.Errorf("GetResetDBOnStart() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetAlfredBatInterface(t *testing.T) {
	tests := []struct {
		name     string
		setValue *string
		want     string
	}{
		{
			name:     "returns configured interface",
			setValue: strPtr("bat1"),
			want:     "bat1",
		},
		{
			name:     "returns default when empty",
			setValue: strPtr(""),
			want:     DefaultAlfredBatInterface,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("alfred.batInterface", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetAlfredBatInterface()
			if got != tt.want {
				t.Errorf("GetAlfredBatInterface() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetAlfredDataTypeNode(t *testing.T) {
	tests := []struct {
		setValue *bool
		name     string
		want     bool
	}{
		{
			name:     "returns true when enabled",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns false when disabled",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultAlfredDataTypeNode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("alfred.dataTypes.node", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetAlfredDataTypeNode()
			if got != tt.want {
				t.Errorf("GetAlfredDataTypeNode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetAlfredDataTypePosition(t *testing.T) {
	tests := []struct {
		setValue *bool
		name     string
		want     bool
	}{
		{
			name:     "returns true when enabled",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns false when disabled",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultAlfredDataTypePosition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("alfred.dataTypes.position", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetAlfredDataTypePosition()
			if got != tt.want {
				t.Errorf("GetAlfredDataTypePosition() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetAlfredDataTypeAddressReservation(t *testing.T) {
	tests := []struct {
		setValue *bool
		name     string
		want     bool
	}{
		{
			name:     "returns true when enabled",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns false when disabled",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultAlfredDataTypeAddressReserv,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("alfred.dataTypes.addressReservation", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetAlfredDataTypeAddressReservation()
			if got != tt.want {
				t.Errorf("GetAlfredDataTypeAddressReservation() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCommsDebug(t *testing.T) {
	tests := []struct {
		setValue *bool
		name     string
		want     bool
	}{
		{
			name:     "returns true when enabled",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns false when disabled",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultCommsDebug,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("comms.debug", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetCommsDebug()
			if got != tt.want {
				t.Errorf("GetCommsDebug() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCommsGPIOSelectorEnable(t *testing.T) {
	tests := []struct {
		setValue *bool
		name     string
		want     bool
	}{
		{
			name:     "returns true when enabled",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns false when disabled",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultCommsGPIOSelectorEnable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("comms.gpioSelector.enable", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetCommsGPIOSelectorEnable()
			if got != tt.want {
				t.Errorf("GetCommsGPIOSelectorEnable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCommsLoopback(t *testing.T) {
	tests := []struct {
		setValue *bool
		name     string
		want     bool
	}{
		{
			name:     "returns true when enabled",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns false when disabled",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultCommsLoopback,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("comms.loopback", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetCommsLoopback()
			if got != tt.want {
				t.Errorf("GetCommsLoopback() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCommsTrace(t *testing.T) {
	tests := []struct {
		setValue *bool
		name     string
		want     bool
	}{
		{
			name:     "returns true when enabled",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns false when disabled",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultCommsTrace,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("comms.trace", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetCommsTrace()
			if got != tt.want {
				t.Errorf("GetCommsTrace() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCommsNanoPTTDevicePath(t *testing.T) {
	tests := []struct {
		name     string
		setValue *string
		want     string
	}{
		{
			name:     "returns configured device path",
			setValue: strPtr("/dev/hidraw1/*"),
			want:     "/dev/hidraw1/*",
		},
		{
			name:     "returns default when empty",
			setValue: strPtr(""),
			want:     DefaultCommsNanoPTTDevicePath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("comms.nanoPTT.devicePath", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetCommsNanoPTTDevicePath()
			if got != tt.want {
				t.Errorf("GetCommsNanoPTTDevicePath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCommsNanoPTTDeviceName(t *testing.T) {
	tests := []struct {
		name     string
		setValue *string
		want     string
	}{
		{
			name:     "returns configured device name",
			setValue: strPtr("Custom NanoPTT Device"),
			want:     "Custom NanoPTT Device",
		},
		{
			name:     "returns default when empty",
			setValue: strPtr(""),
			want:     DefaultCommsNanoPTTDeviceName,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("comms.nanoPTT.deviceName", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetCommsNanoPTTDeviceName()
			if got != tt.want {
				t.Errorf("GetCommsNanoPTTDeviceName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCommsControlSource(t *testing.T) {
	tests := []struct {
		name     string
		setValue *string
		want     string
	}{
		{
			name:     "returns configured control source",
			setValue: strPtr("bluealsa_xevent"),
			want:     "bluealsa_xevent",
		},
		{
			name:     "returns default when empty",
			setValue: strPtr(""),
			want:     DefaultCommsControlSource,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("comms.controlSource", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetCommsControlSource()
			if got != tt.want {
				t.Errorf("GetCommsControlSource() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCommsBluetoothPttBluetoothAudioDeviceHint(t *testing.T) {
	tests := []struct {
		name     string
		setValue *string
		want     string
	}{
		{
			name:     "returns configured audio hint",
			setValue: strPtr("BS-22"),
			want:     "BS-22",
		},
		{
			name:     "returns default when empty",
			setValue: strPtr(""),
			want:     DefaultCommsBluetoothPttBluetoothAudioDeviceHint,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("comms.bluetoothPtt.BluetoothAudioDeviceHint", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetCommsBluetoothPttBluetoothAudioDeviceHint()
			if got != tt.want {
				t.Errorf("GetCommsBluetoothPttBluetoothAudioDeviceHint() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDefaultCommsMicGain pins the bench-derived residual (2026-08-30):
// at the chip's shipped +20 dB analog gain, a loud talker's speech body
// measures ~9.5k ADC counts, so 2.0 keeps it under the 24576 soft knee —
// the knee stays reserved for plosives and transients. The previous 8.0
// hard-clipped every sample at or above 4096 counts.
func TestDefaultCommsMicGain(t *testing.T) {
	if DefaultCommsMicGain != 2.0 {
		t.Fatalf("DefaultCommsMicGain = %v, want 2.0 (bench residual)", DefaultCommsMicGain)
	}
}

func TestGetCommsMicGain(t *testing.T) {
	tests := []struct {
		name     string
		setValue *float64
		want     float32
	}{
		{
			name:     "returns configured gain",
			setValue: float64Ptr(3.0),
			want:     3.0,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultCommsMicGain,
		},
		{
			name:     "returns default when zero",
			setValue: float64Ptr(0),
			want:     DefaultCommsMicGain,
		},
		{
			name:     "returns default when negative",
			setValue: float64Ptr(-1.0),
			want:     DefaultCommsMicGain,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("comms.micGain", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetCommsMicGain()
			if got != tt.want {
				t.Errorf("GetCommsMicGain() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCommsPacketLossPerc(t *testing.T) {
	tests := []struct {
		name     string
		setValue *int
		want     int
	}{
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultCommsPacketLossPerc,
		},
		{
			name:     "returns default when zero (treated as unset)",
			setValue: intPtr(0),
			want:     DefaultCommsPacketLossPerc,
		},
		{
			name:     "returns default when negative (treated as unset)",
			setValue: intPtr(-5),
			want:     DefaultCommsPacketLossPerc,
		},
		{
			name:     "returns configured value mid-range",
			setValue: intPtr(25),
			want:     25,
		},
		{
			name:     "returns configured value at lower clamp",
			setValue: intPtr(CommsPacketLossPercMin),
			want:     CommsPacketLossPercMin,
		},
		{
			name:     "returns configured value at upper clamp",
			setValue: intPtr(CommsPacketLossPercMax),
			want:     CommsPacketLossPercMax,
		},
		{
			name:     "clamps below floor",
			setValue: intPtr(5),
			want:     CommsPacketLossPercMin,
		},
		{
			name:     "clamps above ceiling",
			setValue: intPtr(60),
			want:     CommsPacketLossPercMax,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("comms.packetLossPerc", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetCommsPacketLossPerc()
			if got != tt.want {
				t.Errorf("GetCommsPacketLossPerc() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCommsPlaybackLatencyMs(t *testing.T) {
	tests := []struct {
		name     string
		setValue *int
		want     int
	}{
		{
			name:     "returns configured value",
			setValue: intPtr(80),
			want:     80,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultCommsPlaybackLatencyMs,
		},
		{
			name:     "returns default when zero",
			setValue: intPtr(0),
			want:     DefaultCommsPlaybackLatencyMs,
		},
		{
			name:     "returns default when negative",
			setValue: intPtr(-10),
			want:     DefaultCommsPlaybackLatencyMs,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("comms.playbackLatencyMs", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetCommsPlaybackLatencyMs()
			if got != tt.want {
				t.Errorf("GetCommsPlaybackLatencyMs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCommsCaptureLatencyMs(t *testing.T) {
	tests := []struct {
		name     string
		setValue *int
		want     int
	}{
		{
			name:     "returns configured value",
			setValue: intPtr(80),
			want:     80,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultCommsCaptureLatencyMs,
		},
		{
			name:     "returns default when zero",
			setValue: intPtr(0),
			want:     DefaultCommsCaptureLatencyMs,
		},
		{
			name:     "returns default when negative",
			setValue: intPtr(-10),
			want:     DefaultCommsCaptureLatencyMs,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("comms.captureLatencyMs", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetCommsCaptureLatencyMs()
			if got != tt.want {
				t.Errorf("GetCommsCaptureLatencyMs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCommsCaptureFramesPerBuffer(t *testing.T) {
	tests := []struct {
		name     string
		setValue *int
		want     int
	}{
		{
			name:     "returns configured positive value",
			setValue: intPtr(1920),
			want:     1920,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultCommsCaptureFramesPerBuffer,
		},
		{
			// Unlike most numeric knobs, 0 is an explicit operator choice
			// meaning paFramesPerBufferUnspecified — let PortAudio choose a
			// frame count aligned with the native ALSA period. The reload
			// path uses viper.IsSet to distinguish "not set" (→ default)
			// from "set to 0" (→ pass through).
			name:     "returns zero when explicitly set to zero",
			setValue: intPtr(0),
			want:     0,
		},
		{
			name:     "returns negative value verbatim when explicitly set",
			setValue: intPtr(-1),
			want:     -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("comms.captureFramesPerBuffer", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetCommsCaptureFramesPerBuffer()
			if got != tt.want {
				t.Errorf("GetCommsCaptureFramesPerBuffer() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCommsNanoPTTEnable(t *testing.T) {
	tests := []struct {
		setValue *bool
		name     string
		want     bool
	}{
		{
			name:     "returns true when enabled",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns false when disabled",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultCommsNanoPTTEnable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("comms.nanoPTT.enable", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetCommsNanoPTTEnable()
			if got != tt.want {
				t.Errorf("GetCommsNanoPTTEnable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCommsBluetoothPttEnable(t *testing.T) {
	tests := []struct {
		setValue *bool
		name     string
		want     bool
	}{
		{
			name:     "returns true when enabled",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns false when disabled",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultCommsBluetoothPttEnable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("comms.bluetoothPtt.enable", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetCommsBluetoothPttEnable()
			if got != tt.want {
				t.Errorf("GetCommsBluetoothPttEnable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfigReload(t *testing.T) {
	v := viper.New()
	v.Set("meshNetInterface", "eth0")
	v.Set("gatewayMode", true)

	cfg := New(v)

	// Check initial values
	if got := cfg.GetMeshNetInterface(); got != "eth0" {
		t.Errorf("Initial GetMeshNetInterface() = %v, want eth0", got)
	}

	// Change configuration values
	v.Set("meshNetInterface", "wlh0")

	// Manually trigger reload (simulating config file change)
	cfg.reload()

	// Check updated values
	if got := cfg.GetMeshNetInterface(); got != "wlh0" {
		t.Errorf("After reload GetMeshNetInterface() = %v, want wlh0", got)
	}
}

func TestConfigOnChangeCallback(t *testing.T) {
	v := viper.New()
	v.Set("meshNetInterface", "eth0")

	cfg := New(v)

	callbackCalled := false

	var receivedConfig *Config

	cfg.OnConfigChange(func(c *Config) {
		callbackCalled = true
		receivedConfig = c
	})

	// Change config and trigger reload
	v.Set("meshNetInterface", "wlh0")
	cfg.reload()
	cfg.notifyCallbacks()

	if !callbackCalled {
		t.Error("OnConfigChange callback was not called")
	}

	if receivedConfig != cfg {
		t.Error("Callback did not receive the correct Config instance")
	}

	if got := receivedConfig.GetMeshNetInterface(); got != "wlh0" {
		t.Errorf("Callback config GetMeshNetInterface() = %v, want wlh0", got)
	}
}

func TestGetEnableGNSS(t *testing.T) {
	tests := []struct {
		setValue *bool
		name     string
		want     bool
	}{
		{
			name:     "returns true when set to true",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns false when set to false",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultEnableGNSS,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("gnss.enable", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetEnableGNSS()
			if got != tt.want {
				t.Errorf("GetEnableGNSS() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetGNSSSendAsNMEA(t *testing.T) {
	tests := []struct {
		setValue *bool
		name     string
		want     bool
	}{
		{
			name:     "returns true when enabled",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns false when disabled",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultGNSSSendAsNMEA,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("gnss.sendAsExternalGNSSSource.sendAsNMEA", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetGNSSSendAsNMEA()
			if got != tt.want {
				t.Errorf("GetGNSSSendAsNMEA() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetGNSSSendAsCoT(t *testing.T) {
	tests := []struct {
		setValue *bool
		name     string
		want     bool
	}{
		{
			name:     "returns true when enabled",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns false when disabled",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultGNSSSendAsCoT,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("gnss.sendAsExternalGNSSSource.sendAsCoT", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetGNSSSendAsCoT()
			if got != tt.want {
				t.Errorf("GetGNSSSendAsCoT() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGetBLOSStatusWorkerInterval tests the GetBLOSStatusWorkerInterval method.
func TestGetBLOSStatusWorkerInterval(t *testing.T) {
	tests := []struct {
		name     string
		setValue int
		setKey   bool
		expected int
	}{
		{
			name:     "returns configured interval",
			setValue: 60,
			setKey:   true,
			expected: 60,
		},
		{
			name:     "returns default when zero",
			setValue: 0,
			setKey:   true,
			expected: DefaultBLOSStatusWorkerInterval,
		},
		{
			name:     "returns default when not set",
			setKey:   false,
			expected: DefaultBLOSStatusWorkerInterval,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setKey {
				v.Set("blos.statusWorkerInterval", tt.setValue)
			}

			c := New(v)

			result := c.GetBLOSStatusWorkerInterval()
			if result != tt.expected {
				t.Errorf("GetBLOSStatusWorkerInterval() = %d, want %d", result, tt.expected)
			}
		})
	}
}

// TestBLOSEnabled tests the BLOSEnabled method.
func TestBLOSEnabled(t *testing.T) {
	tests := []struct {
		setValue *bool
		name     string
		want     bool
	}{
		{
			name:     "returns true when enabled",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns false when disabled",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultEnableBLOS,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("blos.enable", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.BLOSEnabled()
			if got != tt.want {
				t.Errorf("BLOSEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetBatmanMulticastForceflood(t *testing.T) {
	tests := []struct {
		setValue *bool
		name     string
		want     bool
	}{
		{
			name:     "returns true when enabled",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns false when disabled",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultBatmanMulticastForceflood,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("batman.multicastForceflood", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetBatmanMulticastForceflood()
			if got != tt.want {
				t.Errorf("GetBatmanMulticastForceflood() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestNewWithoutWatch_IgnoresRemovedMulticastEnhancementsKey pins that a
// config.yml written before batman.multicastEnhancementsEnabled was removed
// (decision D0, 2026-08-27) still loads: viper ignores keys nothing reads,
// and the sibling batman.multicastForceflood key in the same block is still
// honored. This is a regression pin, not a RED test — it passed before the
// key was removed too.
func TestNewWithoutWatch_IgnoresRemovedMulticastEnhancementsKey(t *testing.T) {
	v := viper.New()
	v.SetConfigType("yaml")

	legacy := "batman:\n  multicastEnhancementsEnabled: false\n  multicastForceflood: true\n"
	if err := v.ReadConfig(strings.NewReader(legacy)); err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}

	cfg := NewWithoutWatch(v)

	if !cfg.GetBatmanMulticastForceflood() {
		t.Error("GetBatmanMulticastForceflood() = false; the sibling key must still be read when the removed key is present")
	}
}

// TestDefaultBatmanMulticastForceflood_IsClassicFlooding locks in decision
// D7 (2026-08-27): out-of-the-box deployments classic-flood every multicast
// frame (bat0 multicast_mode 0), matching the LuCI wizard and both fixture
// captures. Changing this default flips the shipped UCI value on every
// device and must be deliberate.
func TestDefaultBatmanMulticastForceflood_IsClassicFlooding(t *testing.T) {
	if !DefaultBatmanMulticastForceflood {
		t.Error("DefaultBatmanMulticastForceflood = false; expected true (classic flooding, multicast_mode 0)")
	}
}

func TestGetBLOSAdvertisedMeshSubnet(t *testing.T) {
	tests := []struct {
		setValue *string
		name     string
		want     string
	}{
		{
			name:     "returns custom CIDR when set",
			setValue: strPtr("10.42.0.0/20"),
			want:     "10.42.0.0/20",
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultBLOSAdvertisedMeshSubnet,
		},
		{
			name:     "falls back to default when malformed",
			setValue: strPtr("not-a-cidr"),
			want:     DefaultBLOSAdvertisedMeshSubnet,
		},
		{
			name:     "falls back to default when empty",
			setValue: strPtr(""),
			want:     DefaultBLOSAdvertisedMeshSubnet,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("blos.advertisedMeshSubnet", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetBLOSAdvertisedMeshSubnet()
			if got != tt.want {
				t.Errorf("GetBLOSAdvertisedMeshSubnet() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetEnableBLOS(t *testing.T) {
	tests := []struct {
		setValue *bool
		name     string
		want     bool
	}{
		{
			name:     "returns true when enabled",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns false when disabled",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultEnableBLOS,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("blos.enable", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetEnableBLOS()
			if got != tt.want {
				t.Errorf("GetEnableBLOS() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetOpenMANETFrontendHostPort(t *testing.T) {
	tests := []struct {
		name     string
		setValue *string
		want     string
	}{
		{
			name:     "returns configured value",
			setValue: strPtr("https://example.com:3000"),
			want:     "https://example.com:3000",
		},
		{
			name:     "returns default when empty",
			setValue: strPtr(""),
			want:     DefaultOpenMANETFrontendHostPort,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultOpenMANETFrontendHostPort,
		},
		{
			name:     "returns custom port",
			setValue: strPtr("http://192.168.1.1:9090"),
			want:     "http://192.168.1.1:9090",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("openmanetFrontendHostPort", *tt.setValue)
			}

			cfg := NewWithoutWatch(v)

			got := cfg.GetOpenMANETFrontendHostPort()
			if got != tt.want {
				t.Errorf("GetOpenMANETFrontendHostPort() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetOpenMANETAPIAddress(t *testing.T) {
	tests := []struct {
		name     string
		setValue *string
		want     string
	}{
		{
			name:     "returns configured value",
			setValue: strPtr("http://127.0.0.1:9000"),
			want:     "http://127.0.0.1:9000",
		},
		{
			name:     "returns default when empty",
			setValue: strPtr(""),
			want:     DefaultOpenMANETAPIAddress,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultOpenMANETAPIAddress,
		},
		{
			name:     "returns custom address",
			setValue: strPtr("http://10.0.0.1:8080"),
			want:     "http://10.0.0.1:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("openmanetAPIAddress", *tt.setValue)
			}

			cfg := NewWithoutWatch(v)

			got := cfg.GetOpenMANETAPIAddress()
			if got != tt.want {
				t.Errorf("GetOpenMANETAPIAddress() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetOpenMANETWebsocketPort(t *testing.T) {
	tests := []struct {
		name     string
		setValue *int
		want     int
	}{
		{
			name:     "returns configured value",
			setValue: intPtr(9090),
			want:     9090,
		},
		{
			name:     "returns default when zero",
			setValue: intPtr(0),
			want:     DefaultOpenMANETWebsocketPort,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultOpenMANETWebsocketPort,
		},
		{
			name:     "returns custom port",
			setValue: intPtr(8443),
			want:     8443,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("openmanetWebsocketPort", *tt.setValue)
			}

			cfg := NewWithoutWatch(v)

			got := cfg.GetOpenMANETWebsocketPort()
			if got != tt.want {
				t.Errorf("GetOpenMANETWebsocketPort() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetOpenMANETAPIAddress(t *testing.T) {
	tests := []struct {
		name         string
		initialValue *string
		setTo        string
		want         string
	}{
		{
			name:         "overrides default",
			initialValue: nil,
			setTo:        "http://192.168.1.10:8087",
			want:         "http://192.168.1.10:8087",
		},
		{
			name:         "overrides configured value",
			initialValue: strPtr("http://10.0.0.1:8080"),
			setTo:        "http://192.168.1.10:8087",
			want:         "http://192.168.1.10:8087",
		},
		{
			name:         "can set to empty string",
			initialValue: strPtr("http://10.0.0.1:8080"),
			setTo:        "",
			want:         "",
		},
		{
			name:         "sets remote address with path",
			initialValue: nil,
			setTo:        "http://remote-host:8087",
			want:         "http://remote-host:8087",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.initialValue != nil {
				v.Set("openmanetAPIAddress", *tt.initialValue)
			}

			cfg := NewWithoutWatch(v)
			cfg.SetOpenMANETAPIAddress(tt.setTo)

			got := cfg.GetOpenMANETAPIAddress()
			if got != tt.want {
				t.Errorf("GetOpenMANETAPIAddress() after Set = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetOpenMANETWebsocketPort(t *testing.T) {
	tests := []struct {
		name         string
		initialValue *int
		setTo        int
		want         int
	}{
		{
			name:         "overrides default",
			initialValue: nil,
			setTo:        3000,
			want:         3000,
		},
		{
			name:         "overrides configured value",
			initialValue: intPtr(9090),
			setTo:        3000,
			want:         3000,
		},
		{
			name:         "can set to zero",
			initialValue: intPtr(9090),
			setTo:        0,
			want:         0,
		},
		{
			name:         "sets high port number",
			initialValue: nil,
			setTo:        65535,
			want:         65535,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.initialValue != nil {
				v.Set("openmanetWebsocketPort", *tt.initialValue)
			}

			cfg := NewWithoutWatch(v)
			cfg.SetOpenMANETWebsocketPort(tt.setTo)

			got := cfg.GetOpenMANETWebsocketPort()
			if got != tt.want {
				t.Errorf("GetOpenMANETWebsocketPort() after Set = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetOpenMANETAPIAddress_DoesNotAffectOtherFields(t *testing.T) {
	v := viper.New()
	v.Set("openmanetWebsocketPort", 9090)
	v.Set("openmanetFrontendHostPort", "http://custom:3000")

	cfg := NewWithoutWatch(v)
	cfg.SetOpenMANETAPIAddress("http://192.168.1.10:8087")

	if got := cfg.GetOpenMANETWebsocketPort(); got != 9090 {
		t.Errorf("GetOpenMANETWebsocketPort() = %v, want 9090", got)
	}

	if got := cfg.GetOpenMANETFrontendHostPort(); got != "http://custom:3000" {
		t.Errorf("GetOpenMANETFrontendHostPort() = %v, want http://custom:3000", got)
	}
}

func TestSetOpenMANETWebsocketPort_DoesNotAffectOtherFields(t *testing.T) {
	v := viper.New()
	v.Set("openmanetAPIAddress", "http://10.0.0.1:8087")
	v.Set("openmanetFrontendHostPort", "http://custom:3000")

	cfg := NewWithoutWatch(v)
	cfg.SetOpenMANETWebsocketPort(3000)

	if got := cfg.GetOpenMANETAPIAddress(); got != "http://10.0.0.1:8087" {
		t.Errorf("GetOpenMANETAPIAddress() = %v, want http://10.0.0.1:8087", got)
	}

	if got := cfg.GetOpenMANETFrontendHostPort(); got != "http://custom:3000" {
		t.Errorf("GetOpenMANETFrontendHostPort() = %v, want http://custom:3000", got)
	}
}

func TestSettersMultipleCallsLastWins(t *testing.T) {
	cfg := NewWithoutWatch(viper.New())

	cfg.SetOpenMANETAPIAddress("http://first:8087")
	cfg.SetOpenMANETAPIAddress("http://second:8087")

	if got := cfg.GetOpenMANETAPIAddress(); got != "http://second:8087" {
		t.Errorf("GetOpenMANETAPIAddress() = %v, want http://second:8087", got)
	}

	cfg.SetOpenMANETWebsocketPort(3000)
	cfg.SetOpenMANETWebsocketPort(4000)

	if got := cfg.GetOpenMANETWebsocketPort(); got != 4000 {
		t.Errorf("GetOpenMANETWebsocketPort() = %v, want 4000", got)
	}
}

func TestGetAlfredEnable(t *testing.T) {
	tests := []struct {
		setValue *bool
		name     string
		want     bool
	}{
		{
			name:     "returns true when enabled",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns false when disabled",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultAlfredEnable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("alfred.enable", *tt.setValue)
			}

			cfg := NewWithoutWatch(v)

			got := cfg.GetAlfredEnable()
			if got != tt.want {
				t.Errorf("GetAlfredEnable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCommsBluetoothPttBluetoothInputDevice(t *testing.T) {
	tests := []struct {
		name     string
		setValue *string
		want     string
	}{
		{
			name:     "returns configured device",
			setValue: strPtr("plughw:1,0"),
			want:     "plughw:1,0",
		},
		{
			name:     "returns default when empty",
			setValue: strPtr(""),
			want:     DefaultCommsBluetoothPttBluetoothInputDevice,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultCommsBluetoothPttBluetoothInputDevice,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("comms.bluetoothPtt.BluetoothInputDevice", *tt.setValue)
			}

			cfg := NewWithoutWatch(v)

			got := cfg.GetCommsBluetoothPttBluetoothInputDevice()
			if got != tt.want {
				t.Errorf("GetCommsBluetoothPttBluetoothInputDevice() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCommsBluetoothPttBluetoothOutputDevice(t *testing.T) {
	tests := []struct {
		name     string
		setValue *string
		want     string
	}{
		{
			name:     "returns configured device",
			setValue: strPtr("plughw:1,0"),
			want:     "plughw:1,0",
		},
		{
			name:     "returns default when empty",
			setValue: strPtr(""),
			want:     DefaultCommsBluetoothPttBluetoothOutputDevice,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultCommsBluetoothPttBluetoothOutputDevice,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("comms.bluetoothPtt.BluetoothOutputDevice", *tt.setValue)
			}

			cfg := NewWithoutWatch(v)

			got := cfg.GetCommsBluetoothPttBluetoothOutputDevice()
			if got != tt.want {
				t.Errorf("GetCommsBluetoothPttBluetoothOutputDevice() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetOpenMANETCommsAPIAddress(t *testing.T) {
	tests := []struct {
		name     string
		setValue *string
		want     string
	}{
		{
			name:     "returns configured value",
			setValue: strPtr("http://10.0.0.1:8087"),
			want:     "http://10.0.0.1:8087",
		},
		{
			name:     "returns default when empty",
			setValue: strPtr(""),
			want:     DefaultOpenMANETCommsAPIAddress,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultOpenMANETCommsAPIAddress,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("openmanetCommsAPIAddress", *tt.setValue)
			}

			cfg := NewWithoutWatch(v)

			got := cfg.GetOpenMANETCommsAPIAddress()
			if got != tt.want {
				t.Errorf("GetOpenMANETCommsAPIAddress() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetOpenMANETCommsAPIAddress(t *testing.T) {
	tests := []struct {
		name         string
		initialValue *string
		setTo        string
		want         string
	}{
		{
			name:         "overrides default",
			initialValue: nil,
			setTo:        "http://192.168.1.10:8087",
			want:         "http://192.168.1.10:8087",
		},
		{
			name:         "overrides configured value",
			initialValue: strPtr("http://10.0.0.1:8080"),
			setTo:        "http://192.168.1.10:8087",
			want:         "http://192.168.1.10:8087",
		},
		{
			name:         "can set to empty string",
			initialValue: strPtr("http://10.0.0.1:8080"),
			setTo:        "",
			want:         "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.initialValue != nil {
				v.Set("openmanetCommsAPIAddress", *tt.initialValue)
			}

			cfg := NewWithoutWatch(v)
			cfg.SetOpenMANETCommsAPIAddress(tt.setTo)

			got := cfg.GetOpenMANETCommsAPIAddress()
			if got != tt.want {
				t.Errorf("GetOpenMANETCommsAPIAddress() after Set = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetOpenMANETCommsAPIAddress_DoesNotAffectOtherFields(t *testing.T) {
	v := viper.New()
	v.Set("openmanetAPIAddress", "0.0.0.0:8087")
	v.Set("openmanetWebsocketPort", 9090)

	cfg := NewWithoutWatch(v)
	cfg.SetOpenMANETCommsAPIAddress("http://192.168.1.10:8087")

	if got := cfg.GetOpenMANETAPIAddress(); got != "0.0.0.0:8087" {
		t.Errorf("GetOpenMANETAPIAddress() = %v, want 0.0.0.0:8087", got)
	}

	if got := cfg.GetOpenMANETWebsocketPort(); got != 9090 {
		t.Errorf("GetOpenMANETWebsocketPort() = %v, want 9090", got)
	}
}
func TestSetOpenMANETFrontendHostPort(t *testing.T) {
	tests := []struct {
		name         string
		initialValue *string
		setTo        string
		want         string
	}{
		{
			name:         "overrides default",
			initialValue: nil,
			setTo:        "http://localhost:3000",
			want:         "http://localhost:3000",
		},
		{
			name:         "overrides configured value",
			initialValue: strPtr("http://custom:8081"),
			setTo:        "http://localhost:3000",
			want:         "http://localhost:3000",
		},
		{
			name:         "can set to empty string",
			initialValue: strPtr("http://custom:8081"),
			setTo:        "",
			want:         "",
		},
		{
			name:         "sets to another custom value",
			initialValue: nil,
			setTo:        "https://example.com:1234",
			want:         "https://example.com:1234",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.initialValue != nil {
				v.Set("openmanetFrontendHostPort", *tt.initialValue)
			}

			cfg := NewWithoutWatch(v)
			cfg.SetOpenMANETFrontendHostPort(tt.setTo)

			got := cfg.GetOpenMANETFrontendHostPort()
			if got != tt.want {
				t.Errorf("GetOpenMANETFrontendHostPort() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetOpenMANETFrontendHostPort_DoesNotAffectOtherFields(t *testing.T) {
	v := viper.New()
	v.Set("openmanetAPIAddress", "http://10.0.0.1:8087")
	v.Set("openmanetWebsocketPort", 9090)

	cfg := NewWithoutWatch(v)
	cfg.SetOpenMANETFrontendHostPort("http://localhost:3000")

	if got := cfg.GetOpenMANETAPIAddress(); got != "http://10.0.0.1:8087" {
		t.Errorf("GetOpenMANETAPIAddress() = %v, want %v", got, "http://10.0.0.1:8087")
	}

	if got := cfg.GetOpenMANETWebsocketPort(); got != 9090 {
		t.Errorf("GetOpenMANETWebsocketPort() = %v, want %v", got, 9090)
	}
}

func TestSetOpenMANETFrontendHostPort_MultipleCalls_LastWins(t *testing.T) {
	v := viper.New()
	cfg := NewWithoutWatch(v)

	cfg.SetOpenMANETFrontendHostPort("http://first:3000")
	cfg.SetOpenMANETFrontendHostPort("http://second:4000")
	cfg.SetOpenMANETFrontendHostPort("http://final:5000")

	got := cfg.GetOpenMANETFrontendHostPort()

	want := "http://final:5000"
	if got != want {
		t.Errorf("GetOpenMANETFrontendHostPort() = %v, want %v", got, want)
	}
}

func TestGetAuthEnable(t *testing.T) {
	tests := []struct {
		name     string
		isSet    bool
		setValue bool
		want     bool
	}{
		{
			name:     "returns true when explicitly enabled",
			isSet:    true,
			setValue: true,
			want:     true,
		},
		{
			name:     "returns false when explicitly disabled",
			isSet:    true,
			setValue: false,
			want:     false,
		},
		{
			name:  "returns default false when not set",
			isSet: false,
			want:  DefaultAuthEnable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.isSet {
				v.Set("auth.enable", tt.setValue)
			}

			cfg := NewWithoutWatch(v)

			got := cfg.GetAuthEnable()
			if got != tt.want {
				t.Errorf("GetAuthEnable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetAuthSessionMaxAgeSecs(t *testing.T) {
	tests := []struct {
		name     string
		setValue *int
		want     int
	}{
		{
			name:     "returns configured value",
			setValue: intPtr(3600),
			want:     3600,
		},
		{
			name:     "returns default when zero",
			setValue: intPtr(0),
			want:     DefaultAuthSessionMaxAgeSecs,
		},
		{
			name:     "returns default when negative",
			setValue: intPtr(-1),
			want:     DefaultAuthSessionMaxAgeSecs,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultAuthSessionMaxAgeSecs,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("auth.sessionMaxAge", *tt.setValue)
			}

			cfg := NewWithoutWatch(v)

			got := cfg.GetAuthSessionMaxAgeSecs()
			if got != tt.want {
				t.Errorf("GetAuthSessionMaxAgeSecs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetAuthSessionMaxSize(t *testing.T) {
	tests := []struct {
		name     string
		setValue *int
		want     int
	}{
		{
			name:     "returns configured value",
			setValue: intPtr(32),
			want:     32,
		},
		{
			name:     "returns default when zero",
			setValue: intPtr(0),
			want:     DefaultAuthSessionMaxSize,
		},
		{
			name:     "returns default when negative",
			setValue: intPtr(-5),
			want:     DefaultAuthSessionMaxSize,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultAuthSessionMaxSize,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("auth.sessionMaxSize", *tt.setValue)
			}

			cfg := NewWithoutWatch(v)

			got := cfg.GetAuthSessionMaxSize()
			if got != tt.want {
				t.Errorf("GetAuthSessionMaxSize() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetAuthPAMService(t *testing.T) {
	tests := []struct {
		name     string
		setValue *string
		want     string
	}{
		{
			name:     "returns configured service name",
			setValue: strPtr("sshd"),
			want:     "sshd",
		},
		{
			name:     "returns default when empty string",
			setValue: strPtr(""),
			want:     DefaultAuthPAMService,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultAuthPAMService,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("auth.pamService", *tt.setValue)
			}

			cfg := NewWithoutWatch(v)

			got := cfg.GetAuthPAMService()
			if got != tt.want {
				t.Errorf("GetAuthPAMService() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAuthDefaults_NothingSet(t *testing.T) {
	v := viper.New()
	cfg := NewWithoutWatch(v)

	if got := cfg.GetAuthEnable(); got != DefaultAuthEnable {
		t.Errorf("GetAuthEnable() = %v, want default %v", got, DefaultAuthEnable)
	}

	if got := cfg.GetAuthSessionMaxAgeSecs(); got != DefaultAuthSessionMaxAgeSecs {
		t.Errorf("GetAuthSessionMaxAgeSecs() = %v, want default %v", got, DefaultAuthSessionMaxAgeSecs)
	}

	if got := cfg.GetAuthSessionMaxSize(); got != DefaultAuthSessionMaxSize {
		t.Errorf("GetAuthSessionMaxSize() = %v, want default %v", got, DefaultAuthSessionMaxSize)
	}

	if got := cfg.GetAuthPAMService(); got != DefaultAuthPAMService {
		t.Errorf("GetAuthPAMService() = %v, want default %v", got, DefaultAuthPAMService)
	}
}

func TestAuthConfig_AllFieldsOverridden(t *testing.T) {
	v := viper.New()
	v.Set("auth.enable", true)
	v.Set("auth.sessionMaxAge", 7200)
	v.Set("auth.sessionMaxSize", 8)
	v.Set("auth.pamService", "system-auth")

	cfg := NewWithoutWatch(v)

	if got := cfg.GetAuthEnable(); !got {
		t.Errorf("GetAuthEnable() = false, want true")
	}

	if got := cfg.GetAuthSessionMaxAgeSecs(); got != 7200 {
		t.Errorf("GetAuthSessionMaxAgeSecs() = %v, want 7200", got)
	}

	if got := cfg.GetAuthSessionMaxSize(); got != 8 {
		t.Errorf("GetAuthSessionMaxSize() = %v, want 8", got)
	}

	if got := cfg.GetAuthPAMService(); got != "system-auth" {
		t.Errorf("GetAuthPAMService() = %v, want system-auth", got)
	}
}

func TestGetInstrumentationEnable(t *testing.T) {
	tests := []struct {
		setValue *bool
		name     string
		want     bool
	}{
		{
			name:     "returns true when enabled",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns false when explicitly disabled",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultInstrumentationEnable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("instrumentation.enable", *tt.setValue)
			}

			cfg := NewWithoutWatch(v)

			got := cfg.GetInstrumentationEnable()
			if got != tt.want {
				t.Errorf("GetInstrumentationEnable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetInstrumentationIntervalSecs(t *testing.T) {
	tests := []struct {
		setValue *int
		name     string
		want     int
	}{
		{
			name:     "returns configured interval",
			setValue: intPtr(60),
			want:     60,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultInstrumentationIntervalSecs,
		},
		{
			name:     "returns default when zero",
			setValue: intPtr(0),
			want:     DefaultInstrumentationIntervalSecs,
		},
		{
			name:     "returns default when negative",
			setValue: intPtr(-1),
			want:     DefaultInstrumentationIntervalSecs,
		},
		{
			name:     "returns 1 when set to minimum positive value",
			setValue: intPtr(1),
			want:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("instrumentation.intervalSecs", *tt.setValue)
			}

			cfg := NewWithoutWatch(v)

			got := cfg.GetInstrumentationIntervalSecs()
			if got != tt.want {
				t.Errorf("GetInstrumentationIntervalSecs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetInstrumentationSnapshotDir(t *testing.T) {
	tests := []struct {
		setValue *string
		name     string
		want     string
	}{
		{
			name:     "returns configured directory",
			setValue: strPtr("/var/log/openmanetd/snapshots"),
			want:     "/var/log/openmanetd/snapshots",
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultInstrumentationSnapshotDir,
		},
		{
			name:     "returns default when empty",
			setValue: strPtr(""),
			want:     DefaultInstrumentationSnapshotDir,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("instrumentation.snapshotDir", *tt.setValue)
			}

			cfg := NewWithoutWatch(v)

			got := cfg.GetInstrumentationSnapshotDir()
			if got != tt.want {
				t.Errorf("GetInstrumentationSnapshotDir() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInstrumentationConfig_AllFieldsOverridden(t *testing.T) {
	v := viper.New()
	v.Set("instrumentation.enable", true)
	v.Set("instrumentation.intervalSecs", 120)
	v.Set("instrumentation.snapshotDir", "/mnt/snapshots")

	cfg := NewWithoutWatch(v)

	if got := cfg.GetInstrumentationEnable(); !got {
		t.Errorf("GetInstrumentationEnable() = false, want true")
	}

	if got := cfg.GetInstrumentationIntervalSecs(); got != 120 {
		t.Errorf("GetInstrumentationIntervalSecs() = %v, want 120", got)
	}

	if got := cfg.GetInstrumentationSnapshotDir(); got != "/mnt/snapshots" {
		t.Errorf("GetInstrumentationSnapshotDir() = %v, want /mnt/snapshots", got)
	}
}

func TestInstrumentationConfig_Defaults(t *testing.T) {
	cfg := NewWithoutWatch(viper.New())

	if got := cfg.GetInstrumentationEnable(); got != DefaultInstrumentationEnable {
		t.Errorf("GetInstrumentationEnable() = %v, want %v", got, DefaultInstrumentationEnable)
	}

	if got := cfg.GetInstrumentationIntervalSecs(); got != DefaultInstrumentationIntervalSecs {
		t.Errorf("GetInstrumentationIntervalSecs() = %v, want %v", got, DefaultInstrumentationIntervalSecs)
	}

	if got := cfg.GetInstrumentationSnapshotDir(); got != DefaultInstrumentationSnapshotDir {
		t.Errorf("GetInstrumentationSnapshotDir() = %v, want %v", got, DefaultInstrumentationSnapshotDir)
	}
}

func TestGetTerminalEnable(t *testing.T) {
	tests := []struct {
		name     string
		setValue *bool
		want     bool
	}{
		{
			name:     "returns configured true",
			setValue: boolPtr(true),
			want:     true,
		},
		{
			name:     "returns configured false",
			setValue: boolPtr(false),
			want:     false,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultTerminalEnable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("terminal.enable", *tt.setValue)
			}

			cfg := NewWithoutWatch(v)
			if got := cfg.GetTerminalEnable(); got != tt.want {
				t.Errorf("GetTerminalEnable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetTerminalShell(t *testing.T) {
	tests := []struct {
		name     string
		setValue *string
		want     string
	}{
		{
			name:     "returns configured value",
			setValue: strPtr("/usr/bin/bash"),
			want:     "/usr/bin/bash",
		},
		{
			name:     "returns default when empty",
			setValue: strPtr(""),
			want:     DefaultTerminalShell,
		},
		{
			name:     "returns default when not set",
			setValue: nil,
			want:     DefaultTerminalShell,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("terminal.shell", *tt.setValue)
			}

			cfg := NewWithoutWatch(v)
			if got := cfg.GetTerminalShell(); got != tt.want {
				t.Errorf("GetTerminalShell() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetSetupEnabled(t *testing.T) {
	tests := []struct {
		name     string
		isSet    bool
		setValue bool
		want     bool
	}{
		{
			name:     "returns true when explicitly enabled",
			isSet:    true,
			setValue: true,
			want:     true,
		},
		{
			name:     "returns false when explicitly disabled",
			isSet:    true,
			setValue: false,
			want:     false,
		},
		{
			name:  "returns default false when not set",
			isSet: false,
			want:  DefaultSetupEnabled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.isSet {
				v.Set("setup.enabled", tt.setValue)
			}

			cfg := NewWithoutWatch(v)

			if got := cfg.GetSetupEnabled(); got != tt.want {
				t.Errorf("GetSetupEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetSetupComplete(t *testing.T) {
	tests := []struct {
		name     string
		isSet    bool
		setValue bool
		want     bool
	}{
		{
			name:     "returns true when explicitly complete",
			isSet:    true,
			setValue: true,
			want:     true,
		},
		{
			name:     "returns false when explicitly incomplete",
			isSet:    true,
			setValue: false,
			want:     false,
		},
		{
			name:  "returns default false when not set",
			isSet: false,
			want:  DefaultSetupComplete,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.isSet {
				v.Set("setup.complete", tt.setValue)
			}

			cfg := NewWithoutWatch(v)

			if got := cfg.GetSetupComplete(); got != tt.want {
				t.Errorf("GetSetupComplete() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCommsDSCP(t *testing.T) {
	tests := []struct {
		name     string
		isSet    bool
		setValue int
		want     int
	}{
		{
			name:  "returns default 46 when not set",
			isSet: false,
			want:  DefaultCommsDSCP,
		},
		{
			name:     "explicit zero disables marking",
			isSet:    true,
			setValue: 0,
			want:     0,
		},
		{
			name:     "returns configured EF value",
			isSet:    true,
			setValue: 46,
			want:     46,
		},
		{
			name:     "returns configured CS6 value",
			isSet:    true,
			setValue: 48,
			want:     48,
		},
		{
			name:     "accepts upper bound 63",
			isSet:    true,
			setValue: 63,
			want:     63,
		},
		{
			name:     "clamps negative to 0",
			isSet:    true,
			setValue: -5,
			want:     0,
		},
		{
			name:     "clamps above 63 to max",
			isSet:    true,
			setValue: 64,
			want:     CommsDSCPMax,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.isSet {
				v.Set("comms.dscp", tt.setValue)
			}

			cfg := NewWithoutWatch(v)

			if got := cfg.GetCommsDSCP(); got != tt.want {
				t.Errorf("GetCommsDSCP() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetAlfredNodeExpiry(t *testing.T) {
	tests := []struct {
		name     string
		setValue *string
		want     time.Duration
	}{
		{name: "returns configured duration", setValue: strPtr("30m"), want: 30 * time.Minute},
		{name: "accepts hours", setValue: strPtr("48h"), want: 48 * time.Hour},
		{name: "zero disables expiry", setValue: strPtr("0"), want: 0},
		{name: "empty falls back to default", setValue: strPtr(""), want: DefaultAlfredNodeExpiry},
		{name: "unparsable falls back to default", setValue: strPtr("soon"), want: DefaultAlfredNodeExpiry},
		{name: "negative falls back to default", setValue: strPtr("-1h"), want: DefaultAlfredNodeExpiry},
		{name: "returns default when not set", setValue: nil, want: DefaultAlfredNodeExpiry},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viper.New()
			if tt.setValue != nil {
				v.Set("alfred.nodeExpiry", *tt.setValue)
			}

			cfg := New(v)

			got := cfg.GetAlfredNodeExpiry()
			if got != tt.want {
				t.Errorf("GetAlfredNodeExpiry() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetAlfredNodeExpiry_NumericZeroFromYAML(t *testing.T) {
	// YAML `nodeExpiry: 0` arrives as an int, not a string.
	v := viper.New()
	v.Set("alfred.nodeExpiry", 0)

	if got := New(v).GetAlfredNodeExpiry(); got != 0 {
		t.Errorf("GetAlfredNodeExpiry() = %v, want 0", got)
	}
}

func TestDefaultAlfredNodeExpiry_Is24h(t *testing.T) {
	if DefaultAlfredNodeExpiry != 24*time.Hour {
		t.Errorf("DefaultAlfredNodeExpiry = %v, want 24h (ledger D4)", DefaultAlfredNodeExpiry)
	}
}
