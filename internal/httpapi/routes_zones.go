package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
)

func registerZones(api huma.API, mux *http.ServeMux, d *Deps) {
	type listIn struct {
		After  string `query:"after" doc:"Opaque cursor"`
		Limit  int    `query:"limit" minimum:"0" maximum:"1000"`
		Offset int    `query:"offset" minimum:"0"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-zones",
		Method:      http.MethodGet,
		Path:        "/v1/zones",
		Summary:     "List cached zones",
		Tags:        []string{"Zones"},
	}, func(ctx context.Context, in *listIn) (*struct{ Body PaginatedZones }, error) {
		if d.Cache == nil {
			return nil, unavailable("zone cache not initialized")
		}
		after := ""
		if in.After != "" {
			var err error
			after, err = decodeZoneCursor(in.After)
			if err != nil {
				return nil, badRequest("invalid after cursor")
			}
		}
		limitVal := in.Limit
		if limitVal <= 0 {
			limitVal = 100
		}
		limit := &limitVal
		var offset *int
		if in.Offset > 0 {
			offset = &in.Offset
		}
		zones, total, next, hasMore := d.Cache.ListZonesPaginated(after, limit, offset)
		out := PaginatedZones{
			Zones:      make([]ZoneSummary, 0, len(zones)),
			TotalCount: total,
			HasMore:    hasMore,
			PageSize:   limit,
		}
		for _, z := range zones {
			out.Zones = append(out.Zones, zoneSummaryFrom(z))
		}
		if next != nil && *next != "" {
			c := encodeZoneCursor(*next)
			out.NextCursor = &c
		}
		enrichDNS(ctx, map[string]any{"zones_count": len(out.Zones), "total_zones": total})
		return &struct{ Body PaginatedZones }{Body: out}, nil
	})

	type zonePath struct {
		Zone string `path:"zone"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-zone",
		Method:      http.MethodGet,
		Path:        "/v1/zones/{zone}",
		Summary:     "Get zone",
		Tags:        []string{"Zones"},
	}, func(ctx context.Context, in *zonePath) (*struct{ Body ZoneDetail }, error) {
		if d.Cache == nil {
			return nil, unavailable("zone cache not initialized")
		}
		zone := normalizeZone(in.Zone)
		cz, err := d.Cache.LoadZone(ctx, zone)
		if err != nil {
			return nil, notFound("Zone '" + zone + "' not found")
		}
		if cz == nil {
			return nil, notFound("Zone '" + zone + "' not found")
		}
		return &struct{ Body ZoneDetail }{Body: zoneDetailFrom(cz)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "refresh-zone",
		Method:      http.MethodPost,
		Path:        "/v1/zones/{zone}/refresh",
		Summary:     "Refresh zone cache",
		Tags:        []string{"Zones"},
	}, func(ctx context.Context, in *zonePath) (*struct{ Body ZoneDetail }, error) {
		if d.Cache == nil {
			return nil, unavailable("zone cache not initialized")
		}
		zone := normalizeZone(in.Zone)
		cz, err := d.Cache.RefreshZone(ctx, zone, false)
		if err != nil {
			return nil, mapDNSUpdateErr(err)
		}
		return &struct{ Body ZoneDetail }{Body: zoneDetailFrom(cz)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "invalidate-zone-cache",
		Method:        http.MethodDelete,
		Path:          "/v1/zones/{zone}/cache",
		Summary:       "Invalidate zone cache",
		Tags:          []string{"Zones"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *zonePath) (*struct{}, error) {
		if d.Cache == nil {
			return nil, unavailable("zone cache not initialized")
		}
		d.Cache.Invalidate(normalizeZone(in.Zone))
		return nil, nil
	})

	mux.HandleFunc("GET /v1/zones/{zone}/export", func(w http.ResponseWriter, r *http.Request) {
		if d.Cache == nil {
			http.Error(w, "zone cache not initialized", http.StatusServiceUnavailable)
			return
		}
		zone := normalizeZone(r.PathValue("zone"))
		cz := d.Cache.PeekZone(zone)
		if cz == nil {
			var err error
			cz, err = d.Cache.LoadZone(r.Context(), zone)
			if err != nil || cz == nil || cz.Zone == nil {
				http.Error(w, "zone not found", http.StatusNotFound)
				return
			}
		}
		text := "$ORIGIN " + zone + "\n" + cz.Zone.ToText()
		filename := dnsx.SanitizeZoneFilename(zone) + ".zone"
		w.Header().Set("Content-Type", "text/dns")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		_, _ = w.Write([]byte(text))
	})
}
