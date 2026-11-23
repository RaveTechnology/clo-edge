package ingress

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strings"
)

type Ownership struct {
	ProjectID                  string
	ProviderName               string
	Attempt                    string
	ServerID                   string
	BootVolumeIDs              []string
	Pending, Deleting, Removed bool
}
type DriverState struct {
	MachineName string
	IPAddress   string
	Ownership   Ownership
	PrivateKey  []byte `json:"-"`
}

func ParseArchive(blob []byte, machine string) (DriverState, error) {
	var out DriverState
	reject := errors.New("invalid bounded current-machine archive")
	if len(blob) == 0 || len(blob) > 8<<20 || !nameRE.MatchString(machine) {
		return out, reject
	}
	gz, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return out, reject
	}
	defer gz.Close()
	expanded, err := io.ReadAll(io.LimitReader(gz, (16<<20)+1))
	if err != nil || len(expanded) > 16<<20 {
		return out, reject
	}
	reader := tar.NewReader(bytes.NewReader(expanded))
	seen := map[string]bool{}
	configs := 0
	keys := 0
	for count := 0; ; count++ {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil || count >= 64 {
			return out, reject
		}
		name := header.Name
		if name == "" || strings.ContainsAny(name, "\\\x00") || seen[name] {
			return out, reject
		}
		// Rancher stores absolute paths inside its machine-state tar. Never
		// extract them; accept only this exact current-machine archive root.
		root := "/home/machine/.docker/machine/machines/" + machine
		if path.IsAbs(name) && name != root && !strings.HasPrefix(name, root+"/") {
			return out, reject
		}
		seen[name] = true
		for _, piece := range strings.Split(name, "/") {
			if piece == ".." {
				return out, reject
			}
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > 1<<20 {
			return out, reject
		}
		payload, err := io.ReadAll(reader)
		if err != nil {
			return out, reject
		}
		switch path.Base(name) {
		case "config.json":
			configs++
			if configs != 1 {
				return out, reject
			}
			var wrapper struct{ Driver DriverState }
			if json.Unmarshal(payload, &wrapper) != nil || wrapper.Driver.MachineName != machine {
				return out, reject
			}
			key := out.PrivateKey
			out = wrapper.Driver
			out.PrivateKey = key
		case "id_rsa":
			keys++
			if keys != 1 || len(payload) > 64<<10 {
				return out, reject
			}
			out.PrivateKey = append([]byte{}, payload...)
		}
	}
	if configs != 1 || out.MachineName != machine {
		return out, reject
	}
	if !out.Ownership.Pending && out.Ownership.ServerID != "" && len(out.Ownership.BootVolumeIDs) > 0 && len(out.PrivateKey) == 0 {
		return out, reject
	}
	return out, nil
}
