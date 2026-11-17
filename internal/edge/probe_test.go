package edge

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrivateProbeChecksIdentityAndRedirect(t *testing.T) {
	for _, body := range []string{"a\n", "b\n"} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		p := &Prober{Timeout: time.Second}
		got := p.private(context.Background(), s.URL, "a")
		s.Close()
		if got != (body == "a\n") {
			t.Fatal("trusted wrong private edge identity")
		}
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "http://127.0.0.1:1", 302) }))
	defer s.Close()
	if (&Prober{Timeout: time.Second}).private(context.Background(), s.URL, "a") {
		t.Fatal("followed redirect")
	}
}

func TestPublicProbePinsIPAndChecksTLS(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "example.com" {
			t.Error("wrong host")
		}
		w.WriteHeader(200)
	}))
	defer s.Close()
	cert, _ := x509.ParseCertificate(s.TLS.Certificates[0].Certificate[0])
	p := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0600)
	// The standard test certificate has example.com as a DNS SAN. DNS is never used.
	host, port, _ := net.SplitHostPort(s.Listener.Addr().String())
	c := testConfig()
	c.PublicHost = "example.com"
	c.CAFile = p
	prober := &Prober{Timeout: time.Second}
	if !prober.public(context.Background(), c, net.JoinHostPort(host, port)) {
		t.Fatal("trusted pinned TLS probe failed")
	}
	c.PublicHost = "wrong.example"
	if prober.public(context.Background(), c, net.JoinHostPort(host, port)) {
		t.Fatal("ignored hostname mismatch")
	}
	c.PublicHost = "example.com"
	c.CAFile = ""
	if prober.public(context.Background(), c, net.JoinHostPort(host, port)) {
		t.Fatal("trusted untrusted certificate")
	}
}
