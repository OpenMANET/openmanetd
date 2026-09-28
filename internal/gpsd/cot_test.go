package gpsd

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for cot.go functions:
// - SendIfRequiredAsCoT
// - checkDeviceActive
// - sendCoTToMulticast
// - sendCoTTAsExternalGPS

func TestSendCameraCoTIfPresent_suppressesRadioMarker(t *testing.T) {
	gps := &GPSService{
		Log:               zerolog.Nop(),
		cameraPresent:     true,
		lastMulticastTime: time.Now(),
	}

	assert.True(t, gps.sendCameraCoTIfPresent(context.Background()), "camera update must suppress the radio marker")

	gps.cameraPresent = false
	assert.False(t, gps.sendCameraCoTIfPresent(context.Background()), "non-camera update must retain the radio path")
}

func TestSendIfRequiredAsCoT_cameraSuppressesNormalPath(t *testing.T) {
	var logs bytes.Buffer

	gps := &GPSService{
		Log:               zerolog.New(&logs),
		cameraPresent:     true,
		lastMulticastTime: time.Now(),
		position:          PositionReport{Valid: true},
	}

	gps.SendIfRequiredAsCoT()

	assert.NotContains(t, logs.String(), "Error getting DHCP leases")
}

func TestSendCameraCoTIfPresent_publishErrorStillOwnsUpdate(t *testing.T) {
	var logs bytes.Buffer

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	gps := &GPSService{
		Log:           zerolog.New(&logs),
		cameraPresent: true,
		position:      PositionReport{Valid: true},
	}

	assert.True(t, gps.sendCameraCoTIfPresent(ctx))
	assert.Contains(t, logs.String(), "Failed to send camera CoT to multicast")
}

func TestReserveCoTMulticastSend(t *testing.T) {
	gps := &GPSService{}

	assert.True(t, gps.reserveCoTMulticastSend())
	assert.False(t, gps.reserveCoTMulticastSend())
}

func TestSendCameraCoTToMulticast_invalidPosition(t *testing.T) {
	gps := &GPSService{}

	err := gps.sendCameraCoTToMulticast(context.Background())
	require.Error(t, err)
	assert.ErrorContains(t, err, "no valid GPS position")
}

func TestSendCoTToMulticast(t *testing.T) {
	log := zerolog.Nop()

	// Create GPS service with valid position and proper initialization
	gps := &GPSService{
		Log: log,
	}

	// Set a valid position
	gps.position = PositionReport{
		Timestamp: time.Now(),
		Latitude:  37.7749,
		Longitude: -122.4194,
		Altitude:  50.0,
		Speed:     5.0,
		Track:     90.0,
		Valid:     true,
		Mode:      3,
	}

	sender := &fakeMulticastSender{}
	gps.SendMulticast = sender.send

	require.NoError(t, gps.sendCoTToMulticast())

	packets := sender.sent()
	require.Len(t, packets, 1)

	typ, uid := decodeMesh(t, packets[0])
	assert.Equal(t, radioUnitType, typ)
	assert.Equal(t, nodeCallsign(), uid)
}

func TestSendCoTToMulticast_InvalidPosition(t *testing.T) {
	log := zerolog.Nop()

	// Create GPS service with invalid position
	gps := &GPSService{
		Log: log,
	}

	gps.position = PositionReport{
		Valid: false,
	}

	err := gps.sendCoTToMulticast()
	if err == nil {
		t.Error("Expected error for invalid position")
	}

	if !strings.Contains(err.Error(), "no valid GPS position") {
		t.Errorf("Expected 'no valid GPS position' error, got: %v", err)
	}
}

func TestCheckDeviceActive_InvalidIP(t *testing.T) {
	log := zerolog.Nop()
	gps := &GPSService{
		Log: log,
	}

	// Test with invalid IP address
	result := gps.checkDeviceActive("invalid-ip")
	if result {
		t.Error("Expected false for invalid IP address")
	}

	// Test with empty IP
	result = gps.checkDeviceActive("")
	if result {
		t.Error("Expected false for empty IP address")
	}

	// Test with malformed IP
	result = gps.checkDeviceActive("999.999.999.999")
	if result {
		t.Error("Expected false for malformed IP address")
	}
}

func TestCheckDeviceActive_NoMatchingInterface(t *testing.T) {
	log := zerolog.Nop()
	gps := &GPSService{
		Log: log,
	}

	// An IP outside every local subnet cannot be a directly-connected EUD.
	result := gps.checkDeviceActive("192.0.2.1") // TEST-NET-1 address
	if result {
		t.Error("Expected false when no local interface contains the address")
	}
}

func TestCheckDeviceActive_Localhost(t *testing.T) {
	log := zerolog.Nop()
	gps := &GPSService{
		Log: log,
	}

	// Loopback is skipped, so no interface matches: not an EUD.
	result := gps.checkDeviceActive("127.0.0.1")
	if result {
		t.Error("Expected false for localhost")
	}
}

func TestSendCoTToMulticast_HAE_Calculation(t *testing.T) {
	log := zerolog.Nop()

	gps := &GPSService{
		Log: log,
		mu:  sync.RWMutex{},
	}

	// Set a position with MSL altitude and geoid separation
	// HAE should be MSL + Geoid Separation
	gps.position = PositionReport{
		Timestamp:       time.Now(),
		Latitude:        40.7128,
		Longitude:       -74.0060,
		Altitude:        10.0, // MSL altitude
		Speed:           5.0,
		Track:           90.0,
		Valid:           true,
		Mode:            3,
		GeoidSeparation: -33.5, // Geoid separation for New York area
	}

	sender := &fakeMulticastSender{}
	gps.SendMulticast = sender.send

	require.NoError(t, gps.sendCoTToMulticast())

	// Test with zero geoid separation (HAE should equal MSL)
	gps.position.GeoidSeparation = 0
	require.NoError(t, gps.sendCoTToMulticast())

	packets := sender.sent()
	require.Len(t, packets, 2)

	assert.InDelta(t, 10.0+(-33.5), decodeMeshHAE(t, packets[0]), 1e-9, "HAE = MSL + geoid separation")
	assert.InDelta(t, 10.0, decodeMeshHAE(t, packets[1]), 1e-9, "HAE = MSL when geoid separation is zero")
}

func TestSendCoTAsExternalGPS_InvalidPosition(t *testing.T) {
	log := zerolog.Nop()

	// Create GPS service with invalid position
	gps := &GPSService{
		Log: log,
	}

	gps.position = PositionReport{
		Valid: false,
	}

	err := gps.sendCoTTAsExternalGPS("192.168.1.100")
	if err == nil {
		t.Error("Expected error for invalid position")
	}

	if !strings.Contains(err.Error(), "no valid GPS position") {
		t.Errorf("Expected 'no valid GPS position' error, got: %v", err)
	}
}

func TestSendCoTAsExternalGPS_ValidPosition(t *testing.T) {
	log := zerolog.Nop()

	gps := &GPSService{
		Log: log,
		mu:  sync.RWMutex{},
	}

	// Set a valid position
	gps.position = PositionReport{
		Timestamp: time.Now(),
		Latitude:  37.7749,
		Longitude: -122.4194,
		Altitude:  50.0,
		Speed:     5.0,
		Track:     90.0,
		Valid:     true,
		Mode:      3,
		HDOP:      1.2,
	}

	// Test that CoT message can be created without errors
	err := gps.sendCoTTAsExternalGPS("192.168.1.100")
	// We don't check for connection errors since the address might not be available in test env
	// Just verify the function doesn't panic and handles the position correctly
	if err != nil && !strings.Contains(err.Error(), "dial") && !strings.Contains(err.Error(), "network") && !strings.Contains(err.Error(), "resolve") {
		t.Errorf("Unexpected error creating CoT message: %v", err)
	}
}

func TestSendCoTAsExternalGPS_HAE_Calculation(t *testing.T) {
	log := zerolog.Nop()

	gps := &GPSService{
		Log: log,
		mu:  sync.RWMutex{},
	}

	// Set a position with MSL altitude and geoid separation
	// HAE should be MSL + Geoid Separation
	gps.position = PositionReport{
		Timestamp:       time.Now(),
		Latitude:        40.7128,
		Longitude:       -74.0060,
		Altitude:        10.0, // MSL altitude
		Speed:           5.0,
		Track:           90.0,
		Valid:           true,
		Mode:            3,
		HDOP:            1.5,
		GeoidSeparation: -33.5, // Geoid separation for New York area
	}

	// The function will create a CoT message
	// We can't easily verify the internal HAE calculation without mocking,
	// but we can at least ensure it doesn't error with geoid separation
	err := gps.sendCoTTAsExternalGPS("192.168.1.100")

	// May get network errors, but shouldn't get position errors
	if err != nil && strings.Contains(err.Error(), "no valid GPS position") {
		t.Errorf("Should not get position error with valid position and geoid separation: %v", err)
	}

	// Test with zero geoid separation (HAE should equal MSL)
	gps.position.GeoidSeparation = 0
	err = gps.sendCoTTAsExternalGPS("192.168.1.100")

	if err != nil && strings.Contains(err.Error(), "no valid GPS position") {
		t.Errorf("Should not get position error with valid position and zero geoid separation: %v", err)
	}
}

func TestSendCoTAsExternalGPS_InvalidIPAddress(t *testing.T) {
	log := zerolog.Nop()

	gps := &GPSService{
		Log: log,
		mu:  sync.RWMutex{},
	}

	// Set a valid position
	gps.position = PositionReport{
		Timestamp: time.Now(),
		Latitude:  37.7749,
		Longitude: -122.4194,
		Altitude:  50.0,
		Speed:     5.0,
		Track:     90.0,
		Valid:     true,
		Mode:      3,
	}

	// Test with invalid IP address format
	err := gps.sendCoTTAsExternalGPS("invalid-ip-address")
	if err == nil {
		t.Error("Expected error for invalid IP address")
	}

	// Should get an error about resolving or dialing
	if !strings.Contains(err.Error(), "resolve") && !strings.Contains(err.Error(), "dial") {
		t.Errorf("Expected resolve or dial error for invalid IP, got: %v", err)
	}
}

func TestSendLocationtoEUDs_NoValidPosition(t *testing.T) {
	log := zerolog.Nop()
	gps := &GPSService{
		Log: log,
		mu:  sync.RWMutex{},
	}

	// Set invalid position
	gps.position = PositionReport{
		Valid: false,
	}

	// Should return early without error
	gps.SendIfRequiredAsCoT()
	// Test passes if no panic occurs
}

// TestSendIfRequiredAsCoT_InvalidPosition tests that the function returns early
// when there is no valid GPS position available
func TestSendIfRequiredAsCoT_InvalidPosition(t *testing.T) {
	log := zerolog.Nop()
	gps := &GPSService{
		Log: log,
		mu:  sync.RWMutex{},
	}

	// Set invalid position
	gps.position = PositionReport{
		Valid: false,
	}

	// Should return early without error or attempting to send
	gps.SendIfRequiredAsCoT()

	// Verify multicast time was not updated (no send attempted)
	gps.mu.RLock()
	lastTime := gps.lastMulticastTime
	gps.mu.RUnlock()

	if !lastTime.IsZero() {
		t.Error("Expected lastMulticastTime to remain zero when position is invalid")
	}
}
