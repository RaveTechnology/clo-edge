package edge

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Server struct{ ID, ProjectID, Status, SwitchStatus string }
type Address struct {
	ID, IP, Status, Type, Entity, Owner string
	External                            bool
	Version, Bandwidth                  int
}
type View struct {
	Servers map[string]Server
	Address Address
}

var ErrUnsafeCloudState = errors.New("confirmed cloud resource binding violation")

func (v View) Validate(c Config) error {
	for _, id := range []string{c.ControllerServerID, c.Edges[0].ID, c.Edges[1].ID} {
		s, ok := v.Servers[id]
		if !ok || s.ID != id || s.ProjectID != c.ProjectID {
			return fmt.Errorf("%w: configured server identity/project missing", ErrUnsafeCloudState)
		}
	}
	a := v.Address
	if a.ID != c.AddressID || a.IP != c.PublicIP || !a.External || a.Version != 4 || a.Bandwidth != c.BandwidthMbps {
		return fmt.Errorf("%w: configured IPv4 identity or bandwidth changed", ErrUnsafeCloudState)
	}
	if a.Type == "FREE" && a.Owner == "" && a.Entity == "" && a.Status == "DOWN" {
		return nil
	}
	if a.Type != "FIXED" || a.Entity != "server" || (a.Owner != c.Edges[0].ID && a.Owner != c.Edges[1].ID) {
		return fmt.Errorf("%w: foreign or unknown address attachment", ErrUnsafeCloudState)
	}
	return nil
}
func (s Server) Stopped() bool { return s.Status == "STOPPED" && s.SwitchStatus == "OFF" }
func (s Server) Running() bool { return s.Status == "ACTIVE" && s.SwitchStatus == "ON" }
func (a Address) Free() bool {
	return a.Status == "DOWN" && a.Type == "FREE" && a.Owner == "" && a.Entity == ""
}
func (a Address) On(id string) bool {
	return a.Status == "ACTIVE" && a.Type == "FIXED" && a.Entity == "server" && a.Owner == id
}

// Powering off a VM changes address liveness without changing its ownership.
func (a Address) OwnedBy(id string) bool {
	return (a.Status == "ACTIVE" || a.Status == "DOWN") && a.Type == "FIXED" && a.Entity == "server" && a.Owner == id
}

type Provider interface {
	Snapshot(context.Context, Config) (View, error)
	Stop(context.Context, string) error
	Detach(context.Context, string) error
	Attach(context.Context, string, string, int) error
}
type Observation struct {
	At      time.Time
	Public  bool
	Private map[string]bool
}
