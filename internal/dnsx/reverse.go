package dnsx

import (
	"fmt"
	"net"
	"strings"

	"github.com/miekg/dns"
)

// ReverseNameFromIP converts an IP address to its full PTR name (with trailing dot).
func ReverseNameFromIP(ip string) (string, error) {
	arpa, err := dns.ReverseAddr(ip)
	if err != nil {
		return "", err
	}
	return arpa, nil
}

// WalkParents finds the managed reverse zone for a PTR name by walking parents.
// managedZones should be lowercase with trailing dots.
func WalkParents(ptrName string, managedZones map[string]struct{}) string {
	name := dns.Fqdn(strings.ToLower(ptrName))
	for {
		if _, ok := managedZones[name]; ok {
			return name
		}
		parent, ok := parentName(name)
		if !ok {
			return ""
		}
		name = parent
	}
}

func parentName(name string) (string, bool) {
	name = dns.Fqdn(name)
	if name == "." {
		return "", false
	}
	labels := dns.SplitDomainName(name)
	if len(labels) <= 1 {
		// single label under root → parent is "."
		if len(labels) == 1 {
			return ".", true
		}
		return "", false
	}
	return dns.Fqdn(strings.Join(labels[1:], ".")), true
}

// RelativePTRLabel returns the relative owner name of ptrName within zone.
func RelativePTRLabel(ptrName, zone string) (string, error) {
	zone = NormalizeZoneName(zone)
	ptr := dns.Fqdn(strings.ToLower(ptrName))
	if !dns.IsSubDomain(zone, ptr) && ptr != zone {
		return "", fmt.Errorf("ptr name %q is outside zone %q", ptr, zone)
	}
	if ptr == zone {
		return "@", nil
	}
	rel := strings.TrimSuffix(ptr, zone)
	rel = strings.TrimSuffix(rel, ".")
	return rel, nil
}

// IsReverseZone reports whether zone is an in-addr.arpa or ip6.arpa zone.
func IsReverseZone(zone string) bool {
	z := strings.ToLower(zone)
	return strings.HasSuffix(z, ".in-addr.arpa.") ||
		strings.HasSuffix(z, ".ip6.arpa.") ||
		strings.HasSuffix(z, ".in-addr.arpa") ||
		strings.HasSuffix(z, ".ip6.arpa")
}

// IPFromReverseName converts a PTR name back to an IP address string.
func IPFromReverseName(ptrName string) (string, error) {
	name := strings.ToLower(strings.TrimSuffix(dns.Fqdn(ptrName), "."))
	labels := dns.SplitDomainName(name)
	if len(labels) < 3 {
		return "", fmt.Errorf("invalid reverse name %q", ptrName)
	}

	// IPv4: n.n.n.n.in-addr.arpa
	if len(labels) >= 6 && labels[len(labels)-2] == "in-addr" && labels[len(labels)-1] == "arpa" {
		octets := labels[:len(labels)-2]
		if len(octets) != 4 {
			return "", fmt.Errorf("invalid IPv4 reverse name %q", ptrName)
		}
		// labels are already reversed relative to IP
		ip := net.ParseIP(fmt.Sprintf("%s.%s.%s.%s", octets[3], octets[2], octets[1], octets[0]))
		if ip == nil {
			return "", fmt.Errorf("invalid IPv4 reverse name %q", ptrName)
		}
		return ip.String(), nil
	}

	// IPv6: 32 nibbles.ip6.arpa
	if len(labels) >= 34 && labels[len(labels)-2] == "ip6" && labels[len(labels)-1] == "arpa" {
		nibbles := labels[:len(labels)-2]
		if len(nibbles) != 32 {
			return "", fmt.Errorf("invalid IPv6 reverse name %q", ptrName)
		}
		var b strings.Builder
		for i := 31; i >= 0; i-- {
			if len(nibbles[i]) != 1 {
				return "", fmt.Errorf("invalid IPv6 reverse name %q", ptrName)
			}
			b.WriteByte(nibbles[i][0])
			if i%4 == 0 && i > 0 {
				b.WriteByte(':')
			}
		}
		ip := net.ParseIP(b.String())
		if ip == nil {
			return "", fmt.Errorf("invalid IPv6 reverse name %q", ptrName)
		}
		return ip.String(), nil
	}

	return "", fmt.Errorf("not a reverse DNS name: %q", ptrName)
}
