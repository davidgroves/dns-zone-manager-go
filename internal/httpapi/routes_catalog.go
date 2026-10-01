package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func registerCatalog(api huma.API, d *Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "catalog-status",
		Method:      http.MethodGet,
		Path:        "/v1/catalog/status",
		Summary:     "Catalog status",
		Tags:        []string{"Catalog"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body map[string]any }, error) {
		if !d.Settings.Catalog.Enabled {
			return &struct{ Body map[string]any }{Body: map[string]any{
				"enabled": false, "connected": false, "zones_discovered": 0,
			}}, nil
		}
		out := map[string]any{
			"enabled":          true,
			"connected":        false,
			"zone_name":        d.Settings.Catalog.ZoneName,
			"zones_discovered": 0,
			"poll_interval":    d.Settings.Catalog.PollInterval,
		}
		if d.Catalog != nil {
			out["connected"] = d.Catalog.Connected()
			out["zone_name"] = d.Catalog.ZoneName()
			if s, ok := d.Catalog.Serial(); ok {
				out["serial"] = s
			}
			if zs, err := d.Catalog.ListZones(); err == nil {
				out["zones_discovered"] = len(zs)
			}
			out["poll_interval"] = d.Catalog.PollInterval()
		}
		return &struct{ Body map[string]any }{Body: out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "catalog-zones",
		Method:      http.MethodGet,
		Path:        "/v1/catalog/zones",
		Summary:     "List catalog zones",
		Tags:        []string{"Catalog"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body map[string]any }, error) {
		zones := []map[string]any{}
		discovered := map[string]struct{}{}
		if d.Catalog != nil {
			if zs, err := d.Catalog.ListZones(); err == nil {
				for _, z := range zs {
					z = normalizeZone(z)
					discovered[z] = struct{}{}
				}
			}
		}
		loaded := map[string]struct{}{}
		if d.Cache != nil {
			for _, z := range d.Cache.ListZones() {
				loaded[z] = struct{}{}
			}
		}
		seen := map[string]struct{}{}
		for z := range discovered {
			seen[z] = struct{}{}
			_, isLoaded := loaded[z]
			zones = append(zones, map[string]any{
				"zone": z, "from_catalog": true, "loaded": isLoaded,
			})
		}
		for z := range loaded {
			if _, ok := seen[z]; ok {
				continue
			}
			zones = append(zones, map[string]any{
				"zone": z, "from_catalog": false, "loaded": true,
			})
		}
		return &struct{ Body map[string]any }{Body: map[string]any{
			"zones": zones, "total": len(zones),
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "catalog-sync",
		Method:      http.MethodPost,
		Path:        "/v1/catalog/sync",
		Summary:     "Sync zones from catalog",
		Tags:        []string{"Catalog"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body map[string]any }, error) {
		if !d.Settings.Catalog.Enabled {
			return nil, badRequest("catalog is disabled")
		}
		if d.Catalog == nil || !d.Catalog.Connected() {
			return nil, unavailable("catalog not connected")
		}
		zs, err := d.Catalog.ListZones()
		if err != nil {
			return nil, unavailable("catalog zone not loaded")
		}
		if d.Cache == nil {
			return nil, unavailable("zone cache not initialized")
		}
		details := d.Cache.SyncFromCatalog(ctx, zs, d.Settings.Catalog.RemoveStaleZones)
		added, exists, failed, removed, synced := 0, 0, 0, 0, 0
		for _, st := range details {
			synced++
			switch st {
			case "added":
				added++
			case "exists":
				exists++
			case "failed":
				failed++
			case "removed":
				removed++
			}
		}
		return &struct{ Body map[string]any }{Body: map[string]any{
			"synced": synced, "added": added, "exists": exists,
			"failed": failed, "removed": removed, "details": details,
		}}, nil
	})
}
