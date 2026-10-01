package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/miekg/dns"

	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
)

func registerReverse(api huma.API, d *Deps) {
	type checkIn struct {
		Body struct {
			SourceName string   `json:"source_name"`
			SourceType string   `json:"source_type" enum:"A,AAAA"`
			Records    []string `json:"records" minItems:"1"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "reverse-ptr-check",
		Method:      http.MethodPost,
		Path:        "/v1/reverse-ptr/check",
		Summary:     "Check reverse PTR feasibility",
		Tags:        []string{"Reverse PTR"},
	}, func(ctx context.Context, in *checkIn) (*struct{ Body map[string]any }, error) {
		managed := managedZones(d)
		results := make([]map[string]any, 0, len(in.Body.Records))
		anyCan := false
		for _, ip := range in.Body.Records {
			res := checkOnePTR(d, ip, managed)
			if can, _ := res["can_create"].(bool); can {
				anyCan = true
			}
			results = append(results, res)
		}
		return &struct{ Body map[string]any }{Body: map[string]any{
			"results": results, "any_can_create": anyCan,
		}}, nil
	})

	type createIn struct {
		Body struct {
			PTRTarget string   `json:"ptr_target"`
			TTL       *uint32  `json:"ttl,omitempty"`
			IPs       []string `json:"ips" minItems:"1"`
			Mode      string   `json:"mode,omitempty" enum:"skip_existing,replace,add_roundrobin"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "reverse-ptr-create",
		Method:      http.MethodPost,
		Path:        "/v1/reverse-ptr",
		Summary:     "Create reverse PTR records",
		Tags:        []string{"Reverse PTR"},
	}, func(ctx context.Context, in *createIn) (*struct{ Body map[string]any }, error) {
		if d.Client == nil {
			return nil, unavailable("DNS client not initialized")
		}
		mode := in.Body.Mode
		if mode == "" {
			mode = "skip_existing"
		}
		ttl := uint32(3600)
		if in.Body.TTL != nil {
			ttl = *in.Body.TTL
		}
		target := in.Body.PTRTarget
		if !strings.HasSuffix(target, ".") {
			target += "."
		}
		managed := managedZones(d)
		results := make([]map[string]any, 0, len(in.Body.IPs))
		created, skipped, errored := 0, 0, 0
		for _, ip := range in.Body.IPs {
			ptrFQDN, err := dnsx.ReverseNameFromIP(ip)
			if err != nil {
				errored++
				results = append(results, map[string]any{
					"ip": ip, "ptr_fqdn": "", "status": "error", "message": err.Error(),
				})
				continue
			}
			revZone := dnsx.WalkParents(ptrFQDN, managed)
			if revZone == "" {
				errored++
				results = append(results, map[string]any{
					"ip": ip, "ptr_fqdn": ptrFQDN, "status": "zone_not_managed",
					"message": "No managed reverse zone found",
				})
				continue
			}
			label, err := dnsx.RelativePTRLabel(ptrFQDN, revZone)
			if err != nil {
				errored++
				results = append(results, map[string]any{
					"ip": ip, "ptr_fqdn": ptrFQDN, "reverse_zone": revZone,
					"status": "error", "message": err.Error(),
				})
				continue
			}
			existing := existingPTRs(d, revZone, ptrFQDN)
			status, msg := "created", "PTR created"
			switch mode {
			case "skip_existing":
				if len(existing) > 0 {
					skipped++
					results = append(results, map[string]any{
						"ip": ip, "ptr_fqdn": ptrFQDN, "reverse_zone": revZone,
						"status": "skipped", "message": "PTR already exists",
					})
					continue
				}
				m, err := d.Client.PrepareAdd(revZone, ptrFQDN, ttl, "PTR", "IN", []string{target}, true)
				if err != nil {
					errored++
					results = append(results, map[string]any{
						"ip": ip, "ptr_fqdn": ptrFQDN, "reverse_zone": revZone,
						"status": "error", "message": err.Error(),
					})
					continue
				}
				if _, err := d.Client.SendUpdate(ctx, revZone, m); err != nil {
					errored++
					results = append(results, map[string]any{
						"ip": ip, "ptr_fqdn": ptrFQDN, "reverse_zone": revZone,
						"status": "error", "message": err.Error(),
					})
					continue
				}
				if d.Cache != nil {
					d.Cache.UpdateCacheAfterAdd(revZone, ptrFQDN, ttl, "PTR", "IN", []string{target})
				}
				created++
			case "replace":
				m, err := d.Client.PrepareReplace(revZone, ptrFQDN, ttl, "PTR", "IN", []string{target}, existing)
				if err != nil {
					// fall back to add
					m, err = d.Client.PrepareAdd(revZone, ptrFQDN, ttl, "PTR", "IN", []string{target}, false)
				}
				if err != nil {
					errored++
					results = append(results, map[string]any{
						"ip": ip, "ptr_fqdn": ptrFQDN, "reverse_zone": revZone,
						"status": "error", "message": err.Error(),
					})
					continue
				}
				if _, err := d.Client.SendUpdate(ctx, revZone, m); err != nil {
					errored++
					results = append(results, map[string]any{
						"ip": ip, "ptr_fqdn": ptrFQDN, "reverse_zone": revZone,
						"status": "error", "message": err.Error(),
					})
					continue
				}
				if d.Cache != nil {
					d.Cache.UpdateCacheAfterReplace(revZone, ptrFQDN, ttl, "PTR", "IN", []string{target})
				}
				status, msg = "replaced", "PTR replaced"
				created++
			case "add_roundrobin":
				m, err := d.Client.PrepareAdd(revZone, ptrFQDN, ttl, "PTR", "IN", []string{target}, false)
				if err != nil {
					errored++
					results = append(results, map[string]any{
						"ip": ip, "ptr_fqdn": ptrFQDN, "reverse_zone": revZone,
						"status": "error", "message": err.Error(),
					})
					continue
				}
				if _, err := d.Client.SendUpdate(ctx, revZone, m); err != nil {
					errored++
					results = append(results, map[string]any{
						"ip": ip, "ptr_fqdn": ptrFQDN, "reverse_zone": revZone,
						"status": "error", "message": err.Error(),
					})
					continue
				}
				if d.Cache != nil {
					d.Cache.UpdateCacheAfterAdd(revZone, ptrFQDN, ttl, "PTR", "IN", []string{target})
				}
				status, msg = "added", "PTR added"
				created++
			}
			_ = label
			_ = dns.TypePTR
			results = append(results, map[string]any{
				"ip": ip, "ptr_fqdn": ptrFQDN, "reverse_zone": revZone,
				"status": status, "message": msg,
			})
		}
		return &struct{ Body map[string]any }{Body: map[string]any{
			"results": results, "created_count": created,
			"skipped_count": skipped, "error_count": errored,
		}}, nil
	})
}

func managedZones(d *Deps) map[string]struct{} {
	out := map[string]struct{}{}
	if d.Cache == nil {
		return out
	}
	for _, z := range d.Cache.ListZones() {
		out[z] = struct{}{}
	}
	return out
}

func checkOnePTR(d *Deps, ip string, managed map[string]struct{}) map[string]any {
	ptrFQDN, err := dnsx.ReverseNameFromIP(ip)
	if err != nil {
		return map[string]any{
			"ip": ip, "ptr_fqdn": "", "zone_managed": false,
			"existing_ptrs": []string{}, "can_create": false, "error": err.Error(),
		}
	}
	revZone := dnsx.WalkParents(ptrFQDN, managed)
	res := map[string]any{
		"ip": ip, "ptr_fqdn": ptrFQDN, "zone_managed": revZone != "",
		"existing_ptrs": []string{}, "can_create": false,
	}
	if revZone == "" {
		return res
	}
	res["reverse_zone"] = revZone
	if label, err := dnsx.RelativePTRLabel(ptrFQDN, revZone); err == nil {
		res["record_name"] = label
	}
	existing := existingPTRs(d, revZone, ptrFQDN)
	res["existing_ptrs"] = existing
	res["can_create"] = true
	return res
}

func existingPTRs(d *Deps, zone, ptrFQDN string) []string {
	if d.Cache == nil {
		return nil
	}
	cz := d.Cache.PeekZone(zone)
	if cz == nil || cz.Zone == nil {
		return nil
	}
	info, ok := cz.Zone.GetRRset(ptrFQDN, dns.TypePTR)
	if !ok || info == nil {
		return nil
	}
	return append([]string(nil), info.Records...)
}
