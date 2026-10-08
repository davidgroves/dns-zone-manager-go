package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
)

func registerAtomic(api huma.API, d *Deps) {
	type op struct {
		Action  string   `json:"action" enum:"add,delete,replace"`
		Name    string   `json:"name"`
		Type    string   `json:"type"`
		RDClass string   `json:"rdclass,omitempty"`
		TTL     *uint32  `json:"ttl,omitempty"`
		Records []string `json:"records,omitempty"`
	}
	type in struct {
		Zone string `path:"zone"`
		Body struct {
			Operations []op `json:"operations" minItems:"1"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "atomic-update",
		Method:      http.MethodPost,
		Path:        "/v1/zones/{zone}/atomic",
		Summary:     "Atomic multi-operation update",
		Tags:        []string{"Atomic"},
	}, func(ctx context.Context, input *in) (*struct{ Body map[string]any }, error) {
		if d.Client == nil || d.Cache == nil {
			return nil, unavailable("DNS not initialized")
		}
		zone := normalizeZone(input.Zone)
		cz := d.Cache.PeekZone(zone)
		if cz == nil {
			loaded, err := d.Cache.LoadZone(ctx, zone)
			if err != nil || loaded == nil {
				return nil, notFound("Zone '" + zone + "' not found")
			}
			cz = loaded
		}

		ops := make([]dnsx.Operation, 0, len(input.Body.Operations))
		for _, o := range input.Body.Operations {
			ttl := uint32(0)
			if o.TTL != nil {
				ttl = *o.TTL
			} else if o.Action != "delete" {
				ttl = 3600
			}
			rdclass := o.RDClass
			if rdclass == "" {
				rdclass = "IN"
			}
			ops = append(ops, dnsx.Operation{
				Action:  strings.ToLower(o.Action),
				Name:    o.Name,
				Type:    strings.ToUpper(o.Type),
				Class:   rdclass,
				TTL:     ttl,
				Records: o.Records,
			})
		}

		lookup := func(name, typ string) (uint32, []string, bool) {
			tc, err := dnsx.TypeFromString(typ)
			if err != nil || cz.Zone == nil {
				return 0, nil, false
			}
			info, ok := cz.Zone.GetRRset(name, tc)
			if !ok {
				return 0, nil, false
			}
			return info.TTL, info.Records, true
		}
		typesAt := func(name string) []string {
			if cz.Zone == nil {
				return nil
			}
			return cz.Zone.TypesAtName(name)
		}

		built, err := dnsx.BuildUpdate(zone, ops, nil, true, true, lookup, typesAt)
		if err != nil {
			return nil, mapDNSUpdateErr(err)
		}
		if _, err := d.Client.SendUpdate(ctx, zone, built.Msg); err != nil {
			return nil, mapDNSUpdateErr(err)
		}
		if cz.Zone != nil {
			_ = dnsx.ApplyCacheUpdates(cz.Zone, built.CacheUpdates)
		}
		d.broadcastLiveOps(zone, "api", dnsx.OperationsFromCacheUpdates(built.CacheUpdates))
		return &struct{ Body map[string]any }{Body: map[string]any{
			"success":          true,
			"zone":             zone,
			"operations_count": len(ops),
			"message":          "Atomic update applied",
		}}, nil
	})
}
