package edge

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

func RenderHAProxy(c Config, id string) (string, error) {
	if e := c.Validate(); e != nil {
		return "", e
	}
	return renderTargets(c, id, c.IngressIPs)
}

// RenderHAProxyBackends updates only the ingress data path, not commissioned VIP configuration.
func RenderHAProxyBackends(c Config, id string, targets []string, generation string) (string, error) {
	if e := c.Validate(); e != nil {
		return "", e
	}
	if len(targets) > 8 || !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{0,63}$`).MatchString(generation) {
		return "", errors.New("invalid bounded publication generation")
	}
	seen := map[string]bool{c.Edges[0].PrivateIP: true, c.Edges[1].PrivateIP: true}
	for _, ip := range targets {
		if !privateIP(ip) || seen[ip] {
			return "", errors.New("unique private backend IPv4s required")
		}
		seen[ip] = true
	}
	out, err := renderTargets(c, id, targets)
	if err != nil {
		return "", err
	}
	out = strings.Replace(out, "global\n", "global\n  stats socket /run/haproxy/clo-ingress-"+generation+".sock mode 600 level operator\n", 1)
	return "# Managed by clo-ingress; generation " + generation + "\n" + out, nil
}

func renderTargets(c Config, id string, targets []string) (string, error) {
	var private string
	for _, e := range c.Edges {
		if e.ID == id {
			private = e.PrivateIP
		}
	}
	if private == "" {
		return "", errors.New("edge ID is not configured")
	}
	var b bytes.Buffer
	b.WriteString("global\n  log stdout format raw local0\n  maxconn 10000\ndefaults\n  log global\n  mode tcp\n  option tcplog\n  timeout connect 3s\n  timeout client 1h\n  timeout server 1h\n")
	for _, port := range []struct {
		name            string
		public, backend int
	}{{"http", 80, 8000}, {"tls", 443, 8443}} {
		fmt.Fprintf(&b, "frontend %s-public\n  bind 0.0.0.0:%d\n  default_backend %s-ingresses\nbackend %s-ingresses\n  balance roundrobin\n  option httpchk GET /ping\n  http-check expect status 200\n", port.name, port.public, port.name, port.name)
		for i, ip := range targets {
			fmt.Fprintf(&b, "  server ingress-%d %s:%d check port 9000 inter 2s fall 2 rise 2 send-proxy-v2\n", i, ip, port.backend)
		}
	}
	fmt.Fprintf(&b, "frontend private-health\n  bind %s:8405\n  mode http\n  option httplog\n  http-request deny deny_status 404 unless { path -m str /healthz }\n  http-request return status 503 content-type text/plain string unavailable if { nbsrv(http-ingresses) lt 1 }\n  http-request return status 503 content-type text/plain string unavailable if { nbsrv(tls-ingresses) lt 1 }\n  http-request return status 200 content-type text/plain lf-string \"%s\\n\"\n", private, id)
	return b.String(), nil
}
