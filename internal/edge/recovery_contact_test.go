package edge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOfflineRecoveryRejectsEveryAnswerFromSource(t *testing.T) {
	for _, kind := range []string{"503", "wrong-id", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "503" {
					w.WriteHeader(503)
				}
				if kind == "truncated" {
					w.Header().Set("Content-Length", "100")
				}
				w.Write([]byte("other-host\n"))
			}))
			defer server.Close()
			healthy, unreachable := (&Prober{Timeout: time.Second}).privateEvidence(context.Background(), server.URL, "expected-source")
			if healthy || unreachable {
				t.Fatal("an HTTP answer is not proof of an unreachable source")
			}
			for _, command := range []string{"attach", "detach"} {
				e, _, now, o := acceptedAttachFixture(t)
				if command == "detach" {
					e, _, now, o = detachFixture(t)
				}
				o.PrivateUnreachable = map[string]bool{e.Config.Edges[0].ID: unreachable}
				var err error
				if command == "attach" {
					_, err = e.CompleteAcceptedAttach(context.Background(), now, o, false)
				} else {
					_, err = e.ResumeAcceptedDetach(context.Background(), now, o, false)
				}
				if err == nil {
					t.Fatal("offline recovery accepted a responding source")
				}
			}
		})
	}
}
func TestPrivateUnreachableRequiresKnownConnectFailure(t *testing.T) {
	s := httptest.NewServer(http.NotFoundHandler())
	u := s.URL
	s.Close()
	p := &Prober{Timeout: time.Second}
	ok, noContact := p.privateEvidence(context.Background(), u, "source")
	if ok || !noContact {
		t.Fatal("refused TCP connection must be recorded separately")
	}
	_, noContact = p.privateEvidence(context.Background(), ":invalid", "source")
	if noContact {
		t.Fatal("unknown transport setup error must not authorize recovery")
	}
}
