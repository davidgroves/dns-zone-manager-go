package config

import (
	"fmt"
	"strings"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// Load reads YAML from path, overlays environment variables, and validates settings.
func Load(path string) (*Settings, error) {
	k := koanf.New(".")
	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		return nil, fmt.Errorf("load config file: %w", err)
	}
	mergeEnv(k)

	var raw map[string]any
	if err := k.Unmarshal("", &raw); err != nil {
		return nil, fmt.Errorf("unmarshal config map: %w", err)
	}
	normalizeRawMap(raw)

	s, err := unmarshalSettings(raw)
	if err != nil {
		return nil, fmt.Errorf("decode settings: %w", err)
	}
	applyPresenceDefaults(k, s)
	applyDefaults(s)
	if err := resolveSecrets(s); err != nil {
		return nil, err
	}
	if err := buildAPIKeyMap(s); err != nil {
		return nil, err
	}
	if err := validate(s); err != nil {
		return nil, err
	}
	return s, nil
}

func normalizeRawMap(raw map[string]any) {
	if keys, ok := raw["api_keys"]; ok {
		if _, exists := raw["api_key"]; !exists {
			raw["api_key"] = keys
		}
		delete(raw, "api_keys")
	}
	apiSection, _ := raw["api_key"].(map[string]any)
	if apiSection == nil {
		return
	}
	if keys, ok := apiSection["keys"].([]any); ok && len(keys) > 0 {
		if _, isMap := keys[0].(map[string]any); isMap {
			return
		}
	}
}

func buildAPIKeyMap(s *Settings) error {
	result := map[string]Secret{}
	if s.APIKey.KeysStr != "" {
		for _, entry := range strings.Split(s.APIKey.KeysStr, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			name, secret, ok := strings.Cut(entry, ":")
			if !ok {
				return fmt.Errorf("invalid API_KEYS entry %q: expected name:secret", entry)
			}
			name = strings.TrimSpace(name)
			sec, err := resolveSecretValue(strings.TrimSpace(secret), "")
			if err != nil {
				return err
			}
			result[name] = sec
		}
	}
	for _, entry := range s.APIKey.KeysList {
		if entry.Name == "" {
			return fmt.Errorf("api_key.keys entry missing name")
		}
		sec, err := resolveSecretField(entry.Secret, entry.SecretFile)
		if err != nil {
			return fmt.Errorf("api_key.keys[%s]: %w", entry.Name, err)
		}
		result[entry.Name] = sec
	}
	s.APIKey.Keys = result
	return nil
}
