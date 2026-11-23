package ingress

import (
	"errors"
	"strings"
)

func VerifyServersState(raw string, targets []string) error {
	reject := errors.New("running HAProxy backend state differs from publication")
	if len(raw) > 1<<20 {
		return reject
	}
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "1" {
		return reject
	}
	header := strings.Fields(strings.TrimPrefix(strings.TrimSpace(lines[1]), "#"))
	columns := map[string]int{}
	for i, name := range header {
		columns[name] = i
	}
	be, ok := columns["be_name"]
	if !ok {
		return reject
	}
	addr, ok := columns["srv_addr"]
	if !ok {
		return reject
	}
	name, ok := columns["srv_name"]
	if !ok {
		return reject
	}
	expected := map[string]bool{}
	for _, target := range targets {
		if expected[target] {
			return reject
		}
		expected[target] = true
	}
	got := map[string]map[string]bool{"http-ingresses": {}, "tls-ingresses": {}}
	names := map[string]bool{}
	for _, line := range lines[2:] {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < len(header) {
			return reject
		}
		backend := fields[be]
		if _, ok := got[backend]; !ok {
			return reject
		}
		if !expected[fields[addr]] || got[backend][fields[addr]] || names[backend+"/"+fields[name]] {
			return reject
		}
		got[backend][fields[addr]] = true
		names[backend+"/"+fields[name]] = true
	}
	for _, values := range got {
		if len(values) != len(expected) {
			return reject
		}
	}
	return nil
}
