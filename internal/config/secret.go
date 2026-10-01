package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Secret holds a sensitive value. Never log String(); use Redacted().
type Secret struct {
	value string
}

func (s Secret) String() string {
	return s.value
}

func (s Secret) Redacted() string {
	if s.IsZero() {
		return ""
	}
	return "***"
}

func (s Secret) IsZero() bool {
	return s.value == ""
}

func (s *Secret) Set(v string) {
	s.value = v
}

// UnmarshalYAML accepts a plain string or null.
func (s *Secret) UnmarshalYAML(value *yaml.Node) error {
	if value == nil {
		return nil
	}
	var raw string
	if err := value.Decode(&raw); err != nil {
		return err
	}
	s.value = raw
	return nil
}

// MarshalYAML redacts on export.
func (s Secret) MarshalYAML() (any, error) {
	if s.IsZero() {
		return nil, nil
	}
	return s.Redacted(), nil
}

func expandEnv(s string) (string, error) {
	var missing []string
	out := envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := match[2 : len(match)-1]
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		missing = append(missing, name)
		return match
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("undefined environment variables in secret: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

func resolveSecretValue(inline string, filePath string) (Secret, error) {
	var parts []string
	if strings.TrimSpace(filePath) != "" {
		b, err := os.ReadFile(filePath)
		if err != nil {
			return Secret{}, fmt.Errorf("read secret file %q: %w", filePath, err)
		}
		parts = append(parts, strings.TrimSpace(string(b)))
	}
	if strings.TrimSpace(inline) != "" {
		parts = append(parts, inline)
	}
	if len(parts) == 0 {
		return Secret{}, nil
	}
	if len(parts) > 1 {
		return Secret{}, fmt.Errorf("secret and secret_file are mutually exclusive")
	}
	expanded, err := expandEnv(parts[0])
	if err != nil {
		return Secret{}, err
	}
	return Secret{value: expanded}, nil
}

func resolveSecretField(inline Secret, filePath string) (Secret, error) {
	return resolveSecretValue(inline.String(), filePath)
}
