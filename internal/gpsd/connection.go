package gpsd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/openmanet/openmanetd/internal/camera"
	"github.com/openmanet/openmanetd/internal/config"
	"github.com/rs/zerolog"
)

// NewGPSService creates a new GPS service that connects to GPSD and monitors TPV reports.
func NewGPSService(log zerolog.Logger, cfg *config.Config) (*GPSService, error) {
	return NewGPSServiceWithAddress(log, cfg, DefaultGPSDAddress)
}

// NewGPSServiceWithAddress creates a new GPS service with a custom GPSD address.
// It creates two separate sessions: one for JSON/TPV reports and one for NMEA sentences.
func NewGPSServiceWithAddress(log zerolog.Logger, cfg *config.Config, address string) (*GPSService, error) {
	done := make(chan struct{})
	cancel := context.CancelFunc(func() {
		select {
		case <-done:
		default:
			close(done)
		}
	})

	cameraPresent, err := camera.Detect(context.Background())
	if err != nil {
		log.Debug().Err(err).Msg("Unable to detect camera for ATAK advertisement")
	}

	g := &GPSService{
		Log:            log,
		Config:         cfg,
		address:        address,
		cameraPresent:  cameraPresent,
		done:           done,
		cancel:         cancel,
		reconnectDelay: 5 * time.Second,
	}

	// Start the connection handler in a goroutine
	go g.connectionHandler()

	// Start the CoT listener, which adopts a directly-connected EUD's
	// broadcast position when gnss.source is set to external_cot. It is
	// always running (cheap, idle otherwise) so toggling the setting at
	// runtime takes effect immediately.
	go g.startCoTListener()

	return g, nil
}

// Close stops the GPS service and closes the connection to GPSD
func (g *GPSService) Close() error {
	if g.cancel != nil {
		g.cancel()
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.conn != nil {
		return g.conn.Close()
	}

	return nil
}

// connectionHandler manages the connection to GPSD with automatic reconnection
func (g *GPSService) connectionHandler() {
	for {
		select {
		case <-g.done:
			return
		default:
			err := g.connect()
			if err != nil {
				g.mu.Lock()
				g.reconnectAttempts++
				attempt := g.reconnectAttempts
				g.mu.Unlock()

				g.Log.Error().Err(err).Int("attempt", attempt).Msg("Failed to connect to GPSD")

				if attempt >= maxReconnectAttempts {
					g.Log.Error().Msg("Maximum reconnection attempts reached, giving up")

					return
				}

				time.Sleep(g.reconnectDelay)

				continue
			}

			// Reset reconnection attempts on successful connection
			g.mu.Lock()
			g.reconnectAttempts = 0
			g.mu.Unlock()

			// Start reading data
			g.readGPSD()

			// If we get here, connection was lost
			g.Log.Warn().Msg("Connection to GPSD lost, reconnecting...")
			time.Sleep(g.reconnectDelay)
		}
	}
}

// connect establishes a connection to GPSD and sends the watch command
func (g *GPSService) connect() error {
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", g.address)
	if err != nil {
		return fmt.Errorf("failed to dial GPSD: %w", err)
	}

	g.mu.Lock()
	g.conn = conn
	g.mu.Unlock()

	// Enable watching for updates with JSON output and raw NMEA sentences
	watchCmd := "?WATCH={\"enable\":true,\"json\":true,\"nmea\":true}\n"

	_, err = conn.Write([]byte(watchCmd))
	if err != nil {
		conn.Close()

		return fmt.Errorf("failed to send watch command: %w", err)
	}

	g.Log.Info().Str("address", g.address).Msg("Connected to GPSD")

	return nil
}

// readGPSD reads and processes data from GPSD
func (g *GPSService) readGPSD() {
	g.mu.RLock()
	conn := g.conn
	g.mu.RUnlock()

	if conn == nil {
		return
	}

	// Whatever ends the read loop (peer hang-up, read error, shutdown),
	// release the descriptor now instead of leaving it to the finalizer
	// while connectionHandler dials a replacement.
	defer func() {
		_ = conn.Close()

		g.mu.Lock()
		if g.conn == conn {
			g.conn = nil
		}
		g.mu.Unlock()
	}()

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		select {
		case <-g.done:
			return
		default:
			line := scanner.Text()
			g.processGPSDMessage(line)
		}
	}

	if err := scanner.Err(); err != nil {
		g.Log.Error().Err(err).Msg("Error reading from GPSD")
	}
}

// processGPSDMessage parses and processes a message from GPSD (JSON or NMEA)
func (g *GPSService) processGPSDMessage(message string) {
	// Check if this is an NMEA sentence (starts with $)
	if len(message) > 0 && message[0] == '$' {
		g.processNMEASentence(message)

		return
	}

	// Try to determine message type by checking class field
	var baseMsg struct {
		Class string `json:"class"`
	}

	err := json.Unmarshal([]byte(message), &baseMsg)
	if err != nil {
		return
	}

	switch baseMsg.Class {
	case gpsdClassTPV:
		var report TPVReport

		err := json.Unmarshal([]byte(message), &report)
		if err != nil {
			return
		}

		g.updatePosition(report)

	case gpsdClassSKY:
		var skyReport SKYReport

		err := json.Unmarshal([]byte(message), &skyReport)
		if err != nil {
			return
		}

		g.updateSatelliteInfo(skyReport)
	}
}

// processNMEASentence handles raw NMEA sentences from GPSD
func (g *GPSService) processNMEASentence(sentence string) {
	// Send NMEA to active devices if configured
	if g.Config != nil && g.Config.GetGNSSSendAsNMEA() {
		go g.sendRawNMEAToActiveDevices(sentence)
	}
}
