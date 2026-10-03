package version

import "testing"

func TestFormat(t *testing.T) {
	t.Parallel()
	cases := []struct {
		exact, nearest, sha, want string
	}{
		{"v0.1.0", "", "", "v0.1.0"},
		{"v0.1", "v0.1.0", "deadbee", "v0.1"},
		{"", "v0.1.0", "abcdef1", "v0.1.0+abcdef1"},
		{"", "v0.1.0", "", unknown},
		{"", "", "abcdef1", unknown},
		{"", "", "", unknown},
		{"  v0.2.0  ", "", "", "v0.2.0"},
	}
	for _, tc := range cases {
		got := Format(tc.exact, tc.nearest, tc.sha)
		if got != tc.want {
			t.Fatalf("Format(%q, %q, %q) = %q, want %q", tc.exact, tc.nearest, tc.sha, got, tc.want)
		}
	}
}

func TestCurrentPrefersLdflags(t *testing.T) {
	prev := Version
	t.Cleanup(func() { Version = prev })
	Version = "v9.9.9"
	if got := Current(); got != "v9.9.9" {
		t.Fatalf("Current() = %q", got)
	}
}

func TestCurrentLooksLikeScheme(t *testing.T) {
	prev := Version
	t.Cleanup(func() { Version = prev })
	Version = ""
	got := Current()
	if got == "" || got[0] != 'v' {
		t.Fatalf("Current() = %q, want v-prefixed", got)
	}
}
