package dnsx

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/miekg/dns"
)

const (
	maxZoneLabels = 127
	maxZoneLen    = 253
)

var safeFilenameRE = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// ZoneNameError indicates an invalid zone or owner name.
type ZoneNameError struct {
	Message string
}

func (e *ZoneNameError) Error() string {
	return e.Message
}

func zoneNameErr(format string, args ...any) error {
	return &ZoneNameError{Message: fmt.Sprintf(format, args...)}
}

// NormalizeZoneName lowercases the name and ensures a trailing dot.
// Invalid names should be rejected with ValidateZoneName first; this function
// performs minimal normalization only.
func NormalizeZoneName(zone string) string {
	zone = strings.TrimSpace(zone)
	if zone == "" {
		return zone
	}
	if !strings.HasSuffix(zone, ".") {
		zone += "."
	}
	return strings.ToLower(zone)
}

// ValidateZoneName checks DNS syntax and length limits for a zone apex name.
func ValidateZoneName(zone string) error {
	if zone == "" || strings.TrimSpace(zone) == "" {
		return zoneNameErr("zone name must not be empty")
	}
	zone = strings.TrimSpace(zone)
	for _, r := range zone {
		if r < 32 || r == 127 {
			return zoneNameErr("zone name must not contain control characters")
		}
	}
	stripped := strings.TrimSuffix(zone, ".")
	if len(stripped) > maxZoneLen {
		return zoneNameErr("zone name exceeds %d characters", maxZoneLen)
	}
	norm := NormalizeZoneName(zone)
	if _, ok := dns.IsDomainName(norm); !ok {
		return zoneNameErr("invalid zone name")
	}
	labels := dns.SplitDomainName(strings.TrimSuffix(norm, "."))
	if len(labels) > maxZoneLabels {
		return zoneNameErr("zone name has too many labels")
	}
	for _, label := range labels {
		if label == "" {
			continue
		}
		if len(label) > 63 {
			return zoneNameErr("zone label exceeds 63 octets")
		}
	}
	return nil
}

// SanitizeZoneFilename builds a safe download filename from a zone name.
func SanitizeZoneFilename(zone string) string {
	base := strings.TrimSuffix(strings.ToLower(zone), ".")
	if base == "" {
		base = "zone"
	}
	safe := strings.Trim(safeFilenameRE.ReplaceAllString(base, "_"), "._")
	if safe == "" {
		safe = "zone"
	}
	return safe + ".zone"
}

// RequireNameInZone resolves @, relative, or absolute names to an FQDN within zone.
func RequireNameInZone(name, zone string) (fqdn string, err error) {
	if err := ValidateZoneName(zone); err != nil {
		return "", err
	}
	zone = NormalizeZoneName(zone)
	name = strings.TrimSpace(name)
	if name == "" || name == "@" {
		return zone, nil
	}

	if strings.HasSuffix(name, ".") {
		fqdn = dns.CanonicalName(name)
	} else if strings.Contains(name, ".") {
		// Unqualified multi-label name is treated as relative to the zone origin.
		fqdn = dns.CanonicalName(name + "." + strings.TrimSuffix(zone, "."))
	} else {
		fqdn = dns.CanonicalName(name + "." + strings.TrimSuffix(zone, "."))
	}

	if fqdn == zone {
		return fqdn, nil
	}
	if dns.IsSubDomain(zone, fqdn) {
		return fqdn, nil
	}
	return "", zoneNameErr("name %q is outside zone %q", fqdn, zone)
}

// IsSubdomain reports whether name is equal to zone or a subdomain of it.
func IsSubdomain(name, zone string) bool {
	name = dns.CanonicalName(name)
	zone = NormalizeZoneName(zone)
	if name == zone {
		return true
	}
	return dns.IsSubDomain(zone, name)
}

// ownerKey converts an FQDN within the zone to an internal map key ("" = apex).
func ownerKey(fqdn, origin string) (string, error) {
	fqdn = dns.CanonicalName(fqdn)
	origin = NormalizeZoneName(origin)
	if fqdn == origin {
		return "", nil
	}
	if !dns.IsSubDomain(origin, fqdn) {
		return "", zoneNameErr("name %q is outside zone %q", fqdn, origin)
	}
	rel := strings.TrimSuffix(fqdn, origin)
	rel = strings.TrimSuffix(rel, ".")
	return strings.ToLower(rel), nil
}

// fqdnFromKey maps an internal owner key back to FQDN.
func fqdnFromKey(key, origin string) string {
	origin = NormalizeZoneName(origin)
	if key == "" || key == "@" {
		return origin
	}
	return dns.CanonicalName(key + "." + strings.TrimSuffix(origin, "."))
}
