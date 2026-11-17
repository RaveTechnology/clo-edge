package edge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

type Edge struct {
	ID        string `json:"id"`
	PrivateIP string `json:"private_ip"`
}
type Config struct {
	ProjectID          string   `json:"project_id"`
	AddressID          string   `json:"address_id"`
	ControllerServerID string   `json:"controller_server_id"`
	PublicIP           string   `json:"public_ip"`
	PublicHost         string   `json:"public_host"`
	HealthPath         string   `json:"health_path"`
	CAFile             string   `json:"ca_file,omitempty"`
	BandwidthMbps      int      `json:"bandwidth_mbps"`
	Edges              [2]Edge  `json:"edges"`
	IngressIPs         []string `json:"ingress_ips"`
	Automatic          bool     `json:"automatic"`
	PollSeconds        int      `json:"poll_seconds,omitempty"`
	ProbeSeconds       int      `json:"probe_seconds,omitempty"`
	SuspectSeconds     int      `json:"suspect_seconds,omitempty"`
	ConvergeSeconds    int      `json:"converge_seconds,omitempty"`
	CooldownSeconds    int      `json:"cooldown_seconds,omitempty"`
}

func strictJSON(raw []byte, dst any) error {
	if len(raw) > 1<<20 {
		return errors.New("JSON exceeds size limit")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.New("invalid or unknown JSON fields")
	}
	if dec.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}
func DecodeConfig(raw []byte) (Config, error) {
	var c Config
	if e := strictJSON(raw, &c); e != nil {
		return c, e
	}
	return c, c.Validate()
}

var uuid = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
var hostname = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9.-]{0,251}[a-zA-Z0-9])?$`)

func privateIP(ip string) bool {
	a, e := netip.ParseAddr(ip)
	return e == nil && a.Is4() && a.IsPrivate()
}
func (c *Config) Validate() error {
	for _, id := range []string{c.ProjectID, c.AddressID, c.ControllerServerID, c.Edges[0].ID, c.Edges[1].ID} {
		if !uuid.MatchString(id) {
			return errors.New("resource IDs must be UUIDs")
		}
	}
	if c.Edges[0].ID == c.Edges[1].ID || c.ControllerServerID == c.Edges[0].ID || c.ControllerServerID == c.Edges[1].ID {
		return errors.New("controller and dedicated edges must be distinct")
	}
	ip, e := netip.ParseAddr(c.PublicIP)
	if e != nil || !ip.Is4() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLoopback() || ip.IsPrivate() {
		return errors.New("public IPv4 literal required")
	}
	if !hostname.MatchString(c.PublicHost) || strings.Contains(c.PublicHost, "..") || strings.ContainsAny(c.PublicHost, "\r\n") {
		return errors.New("invalid public TLS hostname")
	}
	u, e := url.ParseRequestURI(c.HealthPath)
	if e != nil || !strings.HasPrefix(c.HealthPath, "/") || strings.HasPrefix(c.HealthPath, "//") || u.IsAbs() || u.Host != "" || strings.ContainsAny(c.HealthPath, "\r\n") {
		return errors.New("invalid health path")
	}
	if c.BandwidthMbps != 100 && c.BandwidthMbps != 1024 {
		return errors.New("bandwidth must be 100 or 1024 Mbps")
	}
	if len(c.IngressIPs) != 2 {
		return errors.New("exactly two ingress IPv4s required")
	}
	seen := map[string]bool{}
	for _, ip := range []string{c.Edges[0].PrivateIP, c.Edges[1].PrivateIP, c.IngressIPs[0], c.IngressIPs[1]} {
		if !privateIP(ip) || seen[ip] {
			return errors.New("unique private IPv4s required")
		}
		seen[ip] = true
	}
	if c.PollSeconds == 0 {
		c.PollSeconds = 5
	}
	if c.ProbeSeconds == 0 {
		c.ProbeSeconds = 2
	}
	if c.SuspectSeconds == 0 {
		c.SuspectSeconds = 30
	}
	if c.ConvergeSeconds == 0 {
		c.ConvergeSeconds = 180
	}
	if c.CooldownSeconds == 0 {
		c.CooldownSeconds = 600
	}
	if c.PollSeconds < 2 || c.PollSeconds > 60 || c.ProbeSeconds < 1 || c.ProbeSeconds > 10 || c.ProbeSeconds > c.PollSeconds || c.SuspectSeconds < 30 || c.SuspectSeconds > 3600 || c.SuspectSeconds < 3*c.PollSeconds || c.ConvergeSeconds < 60 || c.ConvergeSeconds > 1800 || c.CooldownSeconds < 600 || c.CooldownSeconds > 86400 {
		return errors.New("unsafe timing limits")
	}
	return nil
}
func (c Config) Hash() string {
	raw, _ := json.Marshal(c)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func (c Config) Other(id string) (Edge, bool) {
	if c.Edges[0].ID == id {
		return c.Edges[1], true
	}
	if c.Edges[1].ID == id {
		return c.Edges[0], true
	}
	return Edge{}, false
}
