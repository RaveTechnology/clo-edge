package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
)

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
func (r *LiveRuntime) remote(ctx context.Context, id, ip, key string, worker bool, program string, input any, output any) error {
	if !uuidRE.MatchString(id) {
		return errors.New("invalid SSH host identity")
	}
	address, err := netip.ParseAddr(ip)
	prefix, e := netip.ParsePrefix(r.Config.PrivateCIDR)
	if err != nil || e != nil || !address.Is4() || !prefix.Contains(address) {
		return errors.New("SSH target outside pinned private network")
	}
	if _, err = ReadPrivateFile(key, 1<<20); err != nil {
		return err
	}
	policy := "yes"
	if worker {
		policy = "accept-new"
	}
	args := []string{"-i", key, "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "StrictHostKeyChecking=" + policy, "-o", "HostKeyAlias=" + id, "-o", "UserKnownHostsFile=" + r.Config.KnownHostsFile, "-o", "HostKeyAlgorithms=ssh-ed25519", "-o", "ConnectTimeout=6", "-o", "ServerAliveInterval=5", "-o", "ServerAliveCountMax=2", "root@" + address.String(), "python3 -c " + shellQuote(program)}
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	raw, err := r.Runner.Run(ctx, r.Config.SSH, args, body)
	if err != nil {
		var rejected struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &rejected)
		if nameRE.MatchString(strings.ToLower(rejected.Error)) && len(rejected.Error) < 100 {
			return errors.New("remote preparation rejected: " + rejected.Error)
		}
		return errors.New("authenticated private SSH operation failed; reconcile its durable host state")
	}
	if len(raw) > 1<<20 || json.Unmarshal(raw, output) != nil {
		return errors.New("invalid bounded private SSH acknowledgement")
	}
	return nil
}
