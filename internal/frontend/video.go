package frontend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/digineo/go-uci/v2"
)

const videoConfig = "openmanet_video"

var videoPath = regexp.MustCompile(`^[a-zA-Z0-9._~-]+(/[a-zA-Z0-9._~-]+)*$`)

type videoOptions struct {
	Resolution  string `json:"resolution,omitempty"`
	Hardware    bool   `json:"hardwareAcceleration"`
	Transport   string `json:"transport"`
	Latency     int    `json:"latency"`
	Push        bool   `json:"push"`
	Path        string `json:"path"`
	Destination string `json:"destination"`
}

func option(tree uci.Tree, config, section, key, fallback string) string {
	values, _ := tree.Get(config, section, key)
	if len(values) != 0 && values[0] != "" {
		return values[0]
	}
	return fallback
}

func videoPort(tree uci.Tree, key, fallback string) string {
	addr := option(tree, "mediamtx", "@mediamtx[0]", key, ":"+fallback)
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fallback
	}
	return port
}

func (s *Server) videoSettings(w http.ResponseWriter, _ *http.Request) {
	tree := uci.NewTree(uci.DefaultTreePath)
	get := func(key, def string) string { return option(tree, videoConfig, "playback", key, def) }
	latency, _ := strconv.Atoi(get("srt_latency_ms", "0"))
	_, softErr := os.Stat("/usr/share/mediamtx/software-h264")
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, struct {
		Options           videoOptions `json:"options"`
		CameraPath        string       `json:"cameraPath"`
		SRTPort           string       `json:"srtPort"`
		RTSPPort          string       `json:"rtspPort"`
		SoftwareAvailable bool         `json:"softwareAvailable"`
	}{
		Options: videoOptions{
			Transport:   get("transport", "srt"),
			Resolution:  get("resolution", "current"),
			Latency:     latency,
			Hardware:    get("hardware_acceleration", "1") != "0",
			Push:        get("push_enabled", "0") == "1",
			Path:        get("push_path", ""),
			Destination: get("push_destination", ""),
		},
		CameraPath:        option(tree, "camera-onvif-server", "rpicamera", "rtsp_name", "rpicamera"),
		SRTPort:           videoPort(tree, "srt_address", "8890"),
		RTSPPort:          videoPort(tree, "rtsp_address", "554"),
		SoftwareAvailable: softErr == nil,
	})
}

// srtCallerURL validates FFmpeg-style caller URLs and converts latency to the
// milliseconds used by MediaMTX's Go SRT library for on-demand sources.
func srtCallerURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "srt" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return "", errors.New("enter an SRT caller URL with a host and port")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("invalid SRT port")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || (q.Get("mode") != "" && q.Get("mode") != "caller") {
		return "", errors.New("invalid SRT caller options")
	}
	if q.Has("latency") {
		us, err := strconv.Atoi(q.Get("latency"))
		if err != nil || us < 1000 || us > 8000000 {
			return "", errors.New("URL latency must be 1000–8000000 microseconds")
		}
		q.Set("latency", strconv.Itoa((us+999)/1000))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (s *Server) saveVideoSettings(w http.ResponseWriter, r *http.Request) {
	var v videoOptions
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&v); err != nil || (v.Transport != "srt" && v.Transport != "rtsp") || (v.Latency != 0 && (v.Latency < 20 || v.Latency > 8000)) || (v.Path != "" && !videoPath.MatchString(v.Path)) {
		s.writeErrorStatus(w, 400, "Invalid video settings")
		return
	}
	switch v.Resolution {
	case "", "current", "720p", "1080p":
	default:
		s.writeErrorStatus(w, http.StatusBadRequest, "Unsupported camera resolution")
		return
	}
	if v.Push || v.Destination != "" {
		if _, err := srtCallerURL(v.Destination); err != nil {
			s.writeErrorStatus(w, 400, err.Error())
			return
		}
	}
	s.videoMu.Lock()
	defer s.videoMu.Unlock()
	tree := uci.NewTree(uci.DefaultTreePath)
	enabled := "0"
	if v.Push {
		enabled = "1"
	}
	hardware := "1"
	if !v.Hardware {
		if _, err := os.Stat("/usr/share/mediamtx/software-h264"); err != nil {
			s.writeErrorStatus(w, 400, "Software H.264 encoding is unavailable on this image")
			return
		}
		hardware = "0"
	}
	values := map[string]string{"hardware_acceleration": hardware, "transport": v.Transport, "srt_latency_ms": strconv.Itoa(v.Latency), "push_enabled": enabled, "push_path": v.Path, "push_destination": v.Destination}
	if v.Resolution != "" {
		values["resolution"] = v.Resolution
	} else {
		// Older clients omit this field when changing other settings.
		v.Resolution = option(tree, videoConfig, "playback", "resolution", "current")
	}
	for key, value := range values {
		if err := tree.SetType(videoConfig, "playback", key, uci.TypeOption, value); err != nil {
			s.writeErrorStatus(w, 503, "Video configuration unavailable. Check the MediaMTX package.")
			return
		}
	}
	if err := tree.Commit(); err != nil {
		s.writeErrorStatus(w, 500, "Could not save video settings")
		return
	}
	if _, err := runCmd(r.Context(), "/etc/init.d/mediamtx-srt-push", "reload"); err != nil {
		s.writeErrorStatus(w, 503, "Settings saved. Could not reload the outgoing push service.")
		return
	}
	s.writeJSON(w, v)
}

func mediaRequest(ctx context.Context, method, path string, body []byte) ([]byte, int, error) {
	tree := uci.NewTree(uci.DefaultTreePath)
	endpoint := "http://127.0.0.1:" + videoPort(tree, "api_address", "9997") + path
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode != 200 {
		return nil, resp.StatusCode, fmt.Errorf("MediaMTX returned %d", resp.StatusCode)
	}
	return data, resp.StatusCode, nil
}

func (s *Server) videoStatus(w http.ResponseWriter, r *http.Request) {
	data, _, err := mediaRequest(r.Context(), http.MethodGet, "/v3/srtconns/list", nil)
	if err != nil || !json.Valid(data) {
		s.writeErrorStatus(w, 503, "MediaMTX listener statistics unavailable")
		return
	}
	var push json.RawMessage
	if data, err := os.ReadFile("/var/run/mediamtx-srt-push.json"); err == nil {
		var stamp struct {
			Updated int64 `json:"updated"`
		}
		if json.Unmarshal(data, &stamp) == nil && time.Since(time.Unix(stamp.Updated, 0)) < 15*time.Second {
			push = data
		}
	}
	s.writeJSON(w, map[string]json.RawMessage{"connections": data, "push": push})
}

func (s *Server) videoTransfer(w http.ResponseWriter, r *http.Request) {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		s.writeErrorStatus(w, 415, "Expected JSON")
		return
	}
	var op struct{ Kind, Action, Source string }
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&op) != nil || (op.Kind != "ingest" && op.Kind != "relay") || (op.Action != "open" && op.Action != "close") {
		s.writeErrorStatus(w, 400, "Invalid transfer")
		return
	}
	name := "srt-" + op.Kind
	receipt := "/var/run/openmanet-video-" + op.Kind
	base := "/v3/config/paths/"
	s.videoMu.Lock()
	defer s.videoMu.Unlock()
	if op.Action == "open" {
		path := map[string]any{"source": "publisher", "overridePublisher": false}
		if op.Kind == "relay" {
			source, err := srtCallerURL(op.Source)
			if err != nil {
				s.writeErrorStatus(w, 400, err.Error())
				return
			}
			path = map[string]any{"source": source, "sourceOnDemand": true}
		}
		body, _ := json.Marshal(path)
		if _, _, err := mediaRequest(r.Context(), "POST", base+"add/"+name, body); err != nil {
			s.writeErrorStatus(w, 409, "Could not open the transfer. Check whether the path already exists.")
			return
		}
		conf, _, err := mediaRequest(r.Context(), "GET", base+"get/"+name, nil)
		if err != nil || os.WriteFile(receipt, []byte(fmt.Sprintf("%x", sha256.Sum256(conf))), 0600) != nil {
			_, _, _ = mediaRequest(context.WithoutCancel(r.Context()), "DELETE", base+"delete/"+name, nil)
			s.writeErrorStatus(w, 500, "Could not record transfer ownership")
			return
		}
	} else {
		stamp, err := os.ReadFile(receipt)
		if err != nil {
			s.writeErrorStatus(w, 409, "This UI has no open transfer at this path")
			return
		}
		conf, code, err := mediaRequest(r.Context(), "GET", base+"get/"+name, nil)
		if code != 404 {
			if err != nil || !strings.EqualFold(string(stamp), fmt.Sprintf("%x", sha256.Sum256(conf))) {
				s.writeErrorStatus(w, 409, "Transfer configuration changed or is unavailable")
				return
			}
			if _, _, err = mediaRequest(r.Context(), "DELETE", base+"delete/"+name, nil); err != nil {
				s.writeErrorStatus(w, 503, "Could not close transfer")
				return
			}
		}
		_ = os.Remove(receipt)
	}
	s.writeJSON(w, map[string]string{"path": name})
}
