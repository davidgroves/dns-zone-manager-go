package dnsx

import (
	"testing"
)

func TestNormalizeZoneName(t *testing.T) {
	if got := NormalizeZoneName("Example.COM"); got != "example.com." {
		t.Fatalf("got %q", got)
	}
}

func TestValidateZoneName(t *testing.T) {
	if err := ValidateZoneName("example.com"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateZoneName(""); err == nil {
		t.Fatal("expected error for empty")
	}
	if err := ValidateZoneName("bad\nname.com"); err == nil {
		t.Fatal("expected error for control char")
	}
}

func TestSanitizeZoneFilename(t *testing.T) {
	if got := SanitizeZoneFilename(`evil".com.`); got != "evil_.com.zone" {
		t.Fatalf("got %q", got)
	}
	if got := SanitizeZoneFilename("example.com."); got != "example.com.zone" {
		t.Fatalf("got %q", got)
	}
}

func TestRequireNameInZone(t *testing.T) {
	zone := "example.com."
	cases := []struct {
		name string
		want string
		ok   bool
	}{
		{"@", zone, true},
		{"www", "www.example.com.", true},
		{"www.example.com.", "www.example.com.", true},
		{"www.other.com.", "", false},
	}
	for _, tc := range cases {
		got, err := RequireNameInZone(tc.name, zone)
		if tc.ok && err != nil {
			t.Fatalf("%q: %v", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("%q: expected error", tc.name)
		}
		if tc.ok && got != tc.want {
			t.Fatalf("%q: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestIsSubdomain(t *testing.T) {
	if !IsSubdomain("www.example.com.", "example.com.") {
		t.Fatal("expected subdomain")
	}
	if !IsSubdomain("example.com.", "example.com.") {
		t.Fatal("expected apex match")
	}
	if IsSubdomain("other.net.", "example.com.") {
		t.Fatal("expected false")
	}
}
