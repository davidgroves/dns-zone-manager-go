package catalog

import (
	"sort"
	"strings"

	"github.com/miekg/dns"

	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
)

// CatalogVersion is the RFC 9432 catalog zone version we understand.
const CatalogVersion = "2"

// ExtractMemberZones returns member zone names from PTR records under
// zones.<catalog-origin> per RFC 9432 §2.3. Only direct children of the
// zones label are considered (property PTR trees are ignored).
func ExtractMemberZones(z *dnsx.Zone) []string {
	if z == nil {
		return nil
	}
	origin := dnsx.NormalizeZoneName(z.Origin)
	zonesSuffix := "zones." + origin

	seen := make(map[string]struct{})
	var out []string

	for _, rr := range z.AllRRs() {
		ptr, ok := rr.(*dns.PTR)
		if !ok {
			continue
		}
		name := strings.ToLower(dns.Fqdn(ptr.Hdr.Name))
		if !strings.HasSuffix(name, "."+zonesSuffix) {
			continue
		}
		rest := strings.TrimSuffix(name, "."+zonesSuffix)
		rest = strings.TrimSuffix(rest, ".")
		// Direct member ID only: <unique-token>.zones.<catalog>
		if rest == "" || strings.Contains(rest, ".") {
			continue
		}
		target := dnsx.NormalizeZoneName(ptr.Ptr)
		if target == "" || target == "." {
			continue
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		out = append(out, target)
	}

	sort.Strings(out)
	return out
}

// CatalogVersionTXT returns the apex "version" TXT value if present.
func CatalogVersionTXT(z *dnsx.Zone) (string, bool) {
	if z == nil {
		return "", false
	}
	info, ok := z.GetRRset("version", dns.TypeTXT)
	if !ok || len(info.Records) == 0 {
		return "", false
	}
	// Presentation form may include quotes; strip them.
	v := strings.Trim(info.Records[0], `"`)
	return v, true
}
