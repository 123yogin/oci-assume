// Package ociconfig reads profiles from an OCI CLI config file.
package ociconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotFound is returned when the config file does not exist.
var ErrNotFound = fmt.Errorf("oci config not found: %w", os.ErrNotExist)

// Profile is one section of the config, with DEFAULT merged in.
type Profile struct {
	Name, Region, KeyFile, SecurityTokenFile, Tenancy, User string
}

// IsSession reports whether the profile uses a session token.
func (p Profile) IsSession() bool { return p.SecurityTokenFile != "" }

// DefaultPath returns $OCI_CLI_CONFIG_FILE or ~/.oci/config.
func DefaultPath() string {
	if p := os.Getenv("OCI_CLI_CONFIG_FILE"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".oci", "config")
}

// Find returns the profile with exactly the given name.
func Find(profiles []Profile, name string) (Profile, bool) {
	for _, p := range profiles {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

type section struct {
	name string
	kv   map[string]string
}

// Load parses the file; profiles are returned in file order.
func Load(path string) ([]Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return nil, err
	}
	var secs []*section
	var cur *section
	var def map[string]string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' && line[len(line)-1] == ']' {
			cur = &section{name: strings.TrimSpace(line[1 : len(line)-1]), kv: map[string]string{}}
			secs = append(secs, cur)
			if cur.name == "DEFAULT" {
				def = cur.kv
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || cur == nil {
			continue
		}
		cur.kv[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	dir := filepath.Dir(path)
	out := make([]Profile, 0, len(secs))
	for _, s := range secs {
		get := func(k string) string {
			if v, ok := s.kv[k]; ok {
				return v
			}
			return def[k]
		}
		out = append(out, Profile{
			Name:              s.name,
			Region:            get("region"),
			KeyFile:           resolve(get("key_file"), dir),
			SecurityTokenFile: resolve(get("security_token_file"), dir),
			Tenancy:           get("tenancy"),
			User:              get("user"),
		})
	}
	return out, nil
}

// resolve expands a leading ~ and anchors relative paths at the config dir.
func resolve(p, dir string) string {
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
		return p
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}
