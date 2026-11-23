package ingress

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

var regexpHex = regexp.MustCompile(`^[a-f0-9]{64}$`)

func ReadPrivateFile(path string, maximum int64) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("private file unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) || info.Sys().(*syscall.Stat_t).Nlink != 1 {
		return nil, errors.New("credential file must be private, regular, owned and unlinked")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(raw)) > maximum {
		return nil, errors.New("private file exceeds bound")
	}
	return raw, nil
}
func privateDirectory(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("absolute private directory required")
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return errors.New("directory is not private and owned")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != filepath.Clean(path) {
		return errors.New("symlink in private directory")
	}
	return nil
}
func writePrivate(path string, data []byte) error {
	if old, err := ReadPrivateFile(path, 1<<20); err == nil {
		if string(old) != string(data) {
			return errors.New("identity-bound key changed")
		}
		return nil
	} else if _, err := os.Lstat(path); err == nil {
		return errors.New("existing key is not trusted")
	}
	if len(data) == 0 || len(data) > 1<<20 {
		return errors.New("invalid key size")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".key-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	file.Close()
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
func DecodeConfig(raw []byte) (Config, error) {
	var c Config
	if len(raw) > 1<<20 {
		return c, errors.New("config exceeds bound")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return c, errors.New("invalid or unknown ingress configuration fields")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return c, errors.New("trailing configuration data")
	}
	return c, c.Validate()
}
func (c *Config) ValidateRuntime() error {
	if err := c.Validate(); err != nil {
		return err
	}
	if !uuidRE.MatchString(c.TraefikUID) {
		return errors.New("pinned Traefik DaemonSet UID required")
	}
	if c.TraefikNamespace == "" {
		c.TraefikNamespace = "ingress-system"
	}
	if !nameRE.MatchString(c.TraefikNamespace) {
		return errors.New("invalid Traefik namespace")
	}
	if c.Kubectl == "" {
		c.Kubectl = "/usr/local/bin/kubectl"
	}
	if c.SSH == "" {
		c.SSH = "/usr/bin/ssh"
	}
	if c.PlatformIdentityFile == "" {
		c.PlatformIdentityFile = "/etc/rave-prod-platform.json"
	}
	for _, p := range []string{c.ManagementKubeconfig, c.DownstreamKubeconfig, c.TokenFile, c.EdgeSSHKey, c.KnownHostsFile, c.StateDir, c.Kubectl, c.SSH, c.PlatformIdentityFile} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.ContainsAny(p, "\x00\r\n") {
			return errors.New("explicit canonical absolute runtime paths required")
		}
	}
	for _, p := range []string{c.ManagementKubeconfig, c.DownstreamKubeconfig, c.TokenFile, c.EdgeSSHKey, c.KnownHostsFile} {
		if _, err := ReadPrivateFile(p, 4<<20); err != nil {
			return err
		}
	}
	actual, err := os.ReadFile("/etc/machine-id")
	if err != nil || len(c.ExpectedMachineID) != 32 || strings.TrimSpace(string(actual)) != c.ExpectedMachineID {
		return errors.New("manager is on a different fixed host")
	}
	for _, host := range c.Edge.Edges {
		if !regexpHex.MatchString(c.AdoptionSHA256[host.ID]) || !nameRE.MatchString(c.EdgeIdentityLabels[host.ID]) {
			return errors.New("owned legacy edge adoption hash and marker required")
		}
	}
	for _, id := range c.AdoptNodeUIDs {
		if !uuidRE.MatchString(id) {
			return errors.New("invalid explicit node adoption UID")
		}
	}
	return privateDirectory(c.StateDir)
}
