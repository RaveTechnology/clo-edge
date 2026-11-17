package edge

import (
	"bytes"
	"errors"
	"fmt"
)

func RenderHAProxy(c Config, id string) (string, error) {
	if e := c.Validate(); e != nil {
		return "", e
	}
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
		for i, ip := range c.IngressIPs {
			fmt.Fprintf(&b, "  server ingress-%d %s:%d check port 9000 inter 2s fall 2 rise 2 send-proxy-v2\n", i, ip, port.backend)
		}
	}
	fmt.Fprintf(&b, "frontend private-health\n  bind %s:8405\n  mode http\n  option httplog\n  http-request deny deny_status 404 unless { path -m str /healthz }\n  http-request return status 503 content-type text/plain string unavailable if { nbsrv(http-ingresses) lt 1 }\n  http-request return status 503 content-type text/plain string unavailable if { nbsrv(tls-ingresses) lt 1 }\n  http-request return status 200 content-type text/plain lf-string \"%s\\n\"\n", private, id)
	return b.String(), nil
}
