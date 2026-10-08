package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
	"github.com/davidgroves/dns-zone-manager-go/internal/metrics"
)

func registerRRsets(api huma.API, d *Deps) {
	type writeBody struct {
		Name    string   `json:"name"`
		TTL     *uint32  `json:"ttl,omitempty"`
		Type    string   `json:"type"`
		RDClass string   `json:"rdclass,omitempty"`
		Records []string `json:"records"`
	}
	type zoneBody struct {
		Zone string `path:"zone"`
		Body writeBody
	}

	huma.Register(api, huma.Operation{
		OperationID:   "add-rrset",
		Method:        http.MethodPost,
		Path:          "/v1/zones/{zone}/rrsets",
		Summary:       "Add RRset",
		Tags:          []string{"RRsets"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *zoneBody) (*struct{ Body map[string]any }, error) {
		return mutateRRset(ctx, d, "add", in.Zone, in.Body)
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-rrset",
		Method:      http.MethodDelete,
		Path:        "/v1/zones/{zone}/rrsets",
		Summary:     "Delete RRset",
		Tags:        []string{"RRsets"},
	}, func(ctx context.Context, in *zoneBody) (*struct{ Body map[string]any }, error) {
		return mutateRRset(ctx, d, "delete", in.Zone, in.Body)
	})

	huma.Register(api, huma.Operation{
		OperationID: "replace-rrset",
		Method:      http.MethodPut,
		Path:        "/v1/zones/{zone}/rrsets",
		Summary:     "Replace RRset",
		Tags:        []string{"RRsets"},
	}, func(ctx context.Context, in *zoneBody) (*struct{ Body map[string]any }, error) {
		return mutateRRset(ctx, d, "replace", in.Zone, in.Body)
	})

	type listIn struct {
		Zone   string `path:"zone"`
		Name   string `query:"name"`
		Type   string `query:"type"`
		Class  string `query:"class"`
		After  string `query:"after"`
		Limit  int    `query:"limit" minimum:"0" maximum:"10000"`
		Offset int    `query:"offset" minimum:"0"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-rrsets",
		Method:      http.MethodGet,
		Path:        "/v1/zones/{zone}/rrsets",
		Summary:     "List RRsets",
		Tags:        []string{"RRsets"},
	}, func(ctx context.Context, in *listIn) (*struct{ Body PaginatedRRsets }, error) {
		if d.Cache == nil {
			return nil, unavailable("zone cache not initialized")
		}
		zone := normalizeZone(in.Zone)
		cz := d.Cache.PeekZone(zone)
		if cz == nil {
			var err error
			cz, err = d.Cache.LoadZone(ctx, zone)
			if err != nil || cz == nil {
				return nil, notFound("Zone '" + zone + "' not found")
			}
		}

		// Exact name filter — return matching RRsets (still paginated envelope).
		if in.Name != "" {
			name := in.Name
			if fqdn, err := d.Client.NormalizeName(name, zone); err == nil {
				name = fqdn
			}
			items := []RRsetResponse{}
			if cz.Zone != nil {
				if in.Type != "" {
					typ, err := dnsx.TypeFromString(in.Type)
					if err != nil {
						return nil, badRequest(err.Error())
					}
					if info, ok := cz.Zone.GetRRset(name, typ); ok {
						if in.Class == "" || strings.EqualFold(info.Class, in.Class) {
							items = append(items, rrsetFromInfo(*info))
						}
					}
				} else {
					for _, t := range cz.Zone.TypesAtName(name) {
						typ, _ := dnsx.TypeFromString(t)
						if info, ok := cz.Zone.GetRRset(name, typ); ok {
							items = append(items, rrsetFromInfo(*info))
						}
					}
				}
			}
			return &struct{ Body PaginatedRRsets }{Body: PaginatedRRsets{
				RRsets: items, TotalCount: len(items), HasMore: false,
			}}, nil
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 1000
		}
		offset := in.Offset
		var after *dnsx.RRsetCursor
		if in.After != "" {
			n, t, err := decodeRRsetCursor(in.After)
			if err != nil {
				return nil, badRequest("invalid after cursor")
			}
			after = &dnsx.RRsetCursor{Name: n, Type: t}
		}
		items, total, next, hasMore, ok := d.Cache.GetAllRRsets(zone, offset, limit, after)
		if !ok {
			return nil, notFound("Zone '" + zone + "' not found")
		}
		out := PaginatedRRsets{
			RRsets:     make([]RRsetResponse, 0, len(items)),
			TotalCount: total,
			HasMore:    hasMore,
			PageSize:   &limit,
		}
		for _, it := range items {
			out.RRsets = append(out.RRsets, rrsetFromInfo(it))
		}
		if next != nil {
			c := encodeRRsetCursor(next.Name, next.Type)
			out.NextCursor = &c
		}
		return &struct{ Body PaginatedRRsets }{Body: out}, nil
	})

	type getIn struct {
		Zone       string `path:"zone"`
		Name       string `path:"name"`
		RecordType string `path:"record_type"`
		Class      string `query:"class"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-rrset",
		Method:      http.MethodGet,
		Path:        "/v1/zones/{zone}/rrsets/{name}/{record_type}",
		Summary:     "Get RRset",
		Tags:        []string{"RRsets"},
	}, func(ctx context.Context, in *getIn) (*struct{ Body RRsetResponse }, error) {
		if d.Cache == nil {
			return nil, unavailable("zone cache not initialized")
		}
		zone := normalizeZone(in.Zone)
		cz := d.Cache.PeekZone(zone)
		if cz == nil || cz.Zone == nil {
			return nil, notFound("Zone '" + zone + "' not found")
		}
		typ, err := dnsx.TypeFromString(in.RecordType)
		if err != nil {
			return nil, badRequest(err.Error())
		}
		name := in.Name
		if fqdn, err := d.Client.NormalizeName(name, zone); err == nil {
			name = fqdn
		}
		info, ok := cz.Zone.GetRRset(name, typ)
		if !ok {
			return nil, notFound("RRset not found")
		}
		return &struct{ Body RRsetResponse }{Body: rrsetFromInfo(*info)}, nil
	})
}

func mutateRRset(ctx context.Context, d *Deps, action, zone string, body struct {
	Name    string   `json:"name"`
	TTL     *uint32  `json:"ttl,omitempty"`
	Type    string   `json:"type"`
	RDClass string   `json:"rdclass,omitempty"`
	Records []string `json:"records"`
}) (*struct{ Body map[string]any }, error) {
	if d.Client == nil || d.Cache == nil {
		return nil, unavailable("DNS not initialized")
	}
	zone = normalizeZone(zone)
	rdclass := body.RDClass
	if rdclass == "" {
		rdclass = "IN"
	}
	rdtype := strings.ToUpper(body.Type)
	if _, ok := dnsx.UPDATABLE_TYPES[rdtype]; !ok {
		return nil, badRequest("record type '" + rdtype + "' cannot be modified via API")
	}
	ttl := uint32(3600)
	if body.TTL != nil {
		ttl = *body.TTL
	}

	cz := d.Cache.PeekZone(zone)
	if cz == nil {
		loaded, err := d.Cache.LoadZone(ctx, zone)
		if err != nil || loaded == nil {
			return nil, notFound("Zone '" + zone + "' not found")
		}
		cz = loaded
	}

	name := body.Name
	fqdn, err := d.Client.NormalizeName(name, zone)
	if err != nil {
		return nil, badRequest(err.Error())
	}

	enrichDNS(ctx, map[string]any{"operation": action + "_rrset", "zone": zone, "name": fqdn, "rdtype": rdtype})

	var msg interface { /* placeholder */
	}
	_ = msg
	var updateMsg interface{}
	_ = updateMsg

	liveOp := dnsx.Operation{
		Action:  action,
		Name:    fqdn,
		Type:    rdtype,
		Class:   strings.ToUpper(rdclass),
		TTL:     ttl,
		Records: body.Records,
	}

	switch action {
	case "add":
		// Cache existence check
		typCode, _ := dnsx.TypeFromString(rdtype)
		if info, ok := cz.Zone.GetRRset(fqdn, typCode); ok && info != nil {
			return nil, conflict("RRset already exists")
		}
		m, err := d.Client.PrepareAdd(zone, fqdn, ttl, rdtype, rdclass, body.Records, true)
		if err != nil {
			return nil, badRequest(err.Error())
		}
		if _, err := d.Client.SendUpdate(ctx, zone, m); err != nil {
			return nil, mapDNSUpdateErr(err)
		}
		d.Cache.UpdateCacheAfterAdd(zone, fqdn, ttl, rdtype, rdclass, body.Records)
		metrics.IncRRsetAdds(zone)
	case "delete":
		typCode, _ := dnsx.TypeFromString(rdtype)
		info, ok := cz.Zone.GetRRset(fqdn, typCode)
		if !ok {
			return nil, notFound("RRset not found")
		}
		prereq := info.Records
		m, err := d.Client.PrepareDelete(zone, fqdn, rdtype, rdclass, body.Records, prereq)
		if err != nil {
			return nil, badRequest(err.Error())
		}
		if _, err := d.Client.SendUpdate(ctx, zone, m); err != nil {
			return nil, mapDNSUpdateErr(err)
		}
		d.Cache.UpdateCacheAfterDelete(zone, fqdn, rdtype, rdclass, body.Records)
		metrics.IncRRsetDeletes(zone)
	case "replace":
		typCode, _ := dnsx.TypeFromString(rdtype)
		info, ok := cz.Zone.GetRRset(fqdn, typCode)
		if !ok {
			return nil, notFound("RRset not found; use POST to create")
		}
		m, err := d.Client.PrepareReplace(zone, fqdn, ttl, rdtype, rdclass, body.Records, info.Records)
		if err != nil {
			return nil, badRequest(err.Error())
		}
		if _, err := d.Client.SendUpdate(ctx, zone, m); err != nil {
			return nil, mapDNSUpdateErr(err)
		}
		d.Cache.UpdateCacheAfterReplace(zone, fqdn, ttl, rdtype, rdclass, body.Records)
		metrics.IncRRsetReplaces(zone)
	}
	d.broadcastLiveOps(zone, "api", []dnsx.Operation{liveOp})

	out := map[string]any{"success": true, "message": "RRset " + action + " succeeded"}
	if action != "delete" {
		typCode, _ := dnsx.TypeFromString(rdtype)
		if info, ok := cz.Zone.GetRRset(fqdn, typCode); ok {
			out["rrset"] = rrsetFromInfo(*info)
		}
	}
	return &struct{ Body map[string]any }{Body: out}, nil
}
