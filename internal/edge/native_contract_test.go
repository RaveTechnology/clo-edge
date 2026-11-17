package edge

import "testing"

// Native CLO detail read 17 November 2026: attached external address has type
// FIXED, while attached_to.entity is server. Type is not an entity discriminator.
func TestNativeFixedAddressContract(t *testing.T) {
	c := testConfig()
	c.Validate()
	v := View{Servers: map[string]Server{}, Address: Address{ID: c.AddressID, IP: c.PublicIP, Status: "ACTIVE", Type: "FIXED", Entity: "server", Owner: c.Edges[0].ID, External: true, Version: 4, Bandwidth: 100}}
	for _, id := range []string{c.ControllerServerID, c.Edges[0].ID, c.Edges[1].ID} {
		v.Servers[id] = Server{ID: id, ProjectID: c.ProjectID, Status: "ACTIVE", SwitchStatus: "ON"}
	}
	if err := v.Validate(c); err != nil {
		t.Fatalf("real FIXED/server attachment rejected: %v", err)
	}
	if !v.Address.On(c.Edges[0].ID) {
		t.Fatal("real active FIXED address not recognized")
	}
	v.Address.Status = "DOWN"
	if !v.Address.OwnedBy(c.Edges[0].ID) {
		t.Fatal("powered-off FIXED address lost ownership")
	}
	v.Address.Type = "VROUTER"
	if v.Validate(c) == nil {
		t.Fatal("non-edge address kind accepted")
	}
}
