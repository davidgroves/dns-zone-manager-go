package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// hostCredentials is one API host entry in the dns-cli hosts file.
type hostCredentials struct {
	Token        string    `yaml:"token"`
	RefreshToken string    `yaml:"refresh_token,omitempty"`
	User         string    `yaml:"user,omitempty"`
	ExpiresAt    time.Time `yaml:"expires_at,omitempty"`
	Issuer       string    `yaml:"issuer,omitempty"`
	ClientID     string    `yaml:"client_id,omitempty"`
	Scope        string    `yaml:"scope,omitempty"`
	TokenURL     string    `yaml:"token_endpoint,omitempty"`
}

type hostsFile struct {
	Hosts map[string]hostCredentials `yaml:"hosts"`
}

func configDir() (string, error) {
	if d := os.Getenv("DNS_CLI_CONFIG_DIR"); d != "" {
		return d, nil
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "dns-cli"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "dns-cli"), nil
}

func hostsPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hosts.yml"), nil
}

func hostKey(baseURL string) (string, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid URL: missing host")
	}
	return u.Scheme + "://" + u.Host, nil
}

func loadHosts() (hostsFile, error) {
	path, err := hostsPath()
	if err != nil {
		return hostsFile{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return hostsFile{Hosts: map[string]hostCredentials{}}, nil
		}
		return hostsFile{}, err
	}
	var f hostsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return hostsFile{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Hosts == nil {
		f.Hosts = map[string]hostCredentials{}
	}
	return f, nil
}

func saveHosts(f hostsFile) error {
	path, err := hostsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func lookupHost(baseURL string) (hostCredentials, bool, error) {
	key, err := hostKey(baseURL)
	if err != nil {
		return hostCredentials{}, false, err
	}
	f, err := loadHosts()
	if err != nil {
		return hostCredentials{}, false, err
	}
	c, ok := f.Hosts[key]
	return c, ok, nil
}

func storeHost(baseURL string, c hostCredentials) error {
	key, err := hostKey(baseURL)
	if err != nil {
		return err
	}
	f, err := loadHosts()
	if err != nil {
		return err
	}
	f.Hosts[key] = c
	return saveHosts(f)
}

func deleteHost(baseURL string) error {
	key, err := hostKey(baseURL)
	if err != nil {
		return err
	}
	f, err := loadHosts()
	if err != nil {
		return err
	}
	delete(f.Hosts, key)
	return saveHosts(f)
}
