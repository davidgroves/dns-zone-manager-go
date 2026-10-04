package provision

import (
	"crypto/sha1" //nolint:gosec // RFC 9432 member IDs are unique tokens, not a security hash
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
)

var unsafeFileRE = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

type SOAParams struct {
	Refresh uint32 `json:"refresh,omitempty"`
	Retry   uint32 `json:"retry,omitempty"`
	Expire  uint32 `json:"expire,omitempty"`
	Minimum uint32 `json:"minimum,omitempty"`
}

func zoneFileName(zone string) string {
	base := strings.TrimSuffix(dnsx.NormalizeZoneName(zone), ".")
	if base == "" {
		base = "zone"
	}
	safe := strings.Trim(unsafeFileRE.ReplaceAllString(base, "_"), "._")
	if safe == "" {
		safe = "zone"
	}
	return "db." + safe
}

func joinBindPath(dir, name string) string {
	dir = strings.TrimRight(dir, "/")
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

func localZonePath(localDir, zone string) string {
	return filepath.Join(localDir, zoneFileName(zone))
}

func soaEmail(email string) string {
	email = strings.TrimSpace(email)
	if email == "" {
		return "hostmaster."
	}
	if strings.Contains(email, "@") {
		local, domain, ok := strings.Cut(email, "@")
		if ok {
			email = strings.ReplaceAll(local, ".", "\\.") + "." + domain
		}
	}
	return dnsx.NormalizeZoneName(email)
}

func nsName(name string) string {
	return dnsx.NormalizeZoneName(strings.TrimSpace(name))
}

func memberLabel(zone string) string {
	zone = dnsx.NormalizeZoneName(zone)
	sum := sha1.Sum([]byte(zone)) //nolint:gosec
	return hex.EncodeToString(sum[:])
}

func renderSeedZone(zone string, d config.RNDCZoneDefaults, req CreateRequest, serial uint32) string {
	zone = dnsx.NormalizeZoneName(zone)
	ttl := d.TTL
	if req.TTL != nil && *req.TTL > 0 {
		ttl = *req.TTL
	}
	primary := nsName(d.PrimaryNS)
	if req.PrimaryNS != "" {
		primary = nsName(req.PrimaryNS)
	}
	admin := soaEmail(d.AdminEmail)
	if req.AdminEmail != "" {
		admin = soaEmail(req.AdminEmail)
	}
	refresh, retry, expire, minimum := d.Refresh, d.Retry, d.Expire, d.Minimum
	if req.SOA != nil {
		if req.SOA.Refresh > 0 {
			refresh = req.SOA.Refresh
		}
		if req.SOA.Retry > 0 {
			retry = req.SOA.Retry
		}
		if req.SOA.Expire > 0 {
			expire = req.SOA.Expire
		}
		if req.SOA.Minimum > 0 {
			minimum = req.SOA.Minimum
		}
	}
	nameservers := req.Nameservers
	if len(nameservers) == 0 {
		nameservers = d.Nameservers
	}
	if len(nameservers) == 0 && primary != "" && primary != "." {
		nameservers = []string{primary}
	}
	if serial == 0 {
		serial = uint32(time.Now().Unix()) //nolint:gosec
	}

	var b strings.Builder
	fmt.Fprintf(&b, "$ORIGIN %s\n$TTL %d\n", zone, ttl)
	fmt.Fprintf(&b, "@ IN SOA %s %s (\n", primary, admin)
	fmt.Fprintf(&b, "    %d %d %d %d %d )\n", serial, refresh, retry, expire, minimum)
	for _, ns := range nameservers {
		n := nsName(ns)
		if n == "" || n == "." {
			continue
		}
		fmt.Fprintf(&b, "@ IN NS %s\n", n)
	}
	return b.String()
}

func renderAddzoneConfig(zone string, r config.RNDCSettings) string {
	fileName := zoneFileName(zone)
	var parts []string
	parts = append(parts, "type primary")
	switch strings.ToLower(r.ZoneSeed.Mode) {
	case config.RNDCSeedInitialFile:
		bindFile := joinBindPath(r.ZoneSeed.BindDir, fileName)
		if r.ZoneSeed.BindDir == "" {
			bindFile = fileName
		}
		parts = append(parts, fmt.Sprintf(`file "%s"`, bindFile))
		parts = append(parts, fmt.Sprintf(`initial-file "%s"`, r.ZoneSeed.InitialFile))
	default:
		parts = append(parts, fmt.Sprintf(`file "%s"`, joinBindPath(r.ZoneSeed.BindDir, fileName)))
	}
	upd := r.ZoneTemplate.AllowUpdateKey
	xfer := r.ZoneTemplate.AllowTransferKey
	if upd != "" {
		parts = append(parts, fmt.Sprintf(`allow-update { key "%s"; }`, upd))
	}
	if xfer != "" {
		parts = append(parts, fmt.Sprintf(`allow-transfer { key "%s"; }`, xfer))
	}
	if extra := strings.TrimSpace(r.ZoneTemplate.Extra); extra != "" {
		extra = strings.TrimSuffix(strings.TrimSpace(extra), ";")
		parts = append(parts, extra)
	}
	return "{ " + strings.Join(parts, "; ") + "; }"
}
