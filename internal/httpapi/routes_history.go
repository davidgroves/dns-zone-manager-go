package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/miekg/dns"

	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
)

func registerHistory(api huma.API, d *Deps) {
	type histIn struct {
		Zone       string `path:"zone"`
		FromSerial int64  `query:"from_serial" default:"-1"` // -1 = RFC 1982 max history
	}
	huma.Register(api, huma.Operation{
		OperationID: "zone-history",
		Method:      http.MethodGet,
		Path:        "/v1/zones/{zone}/history",
		Summary:     "Get zone history via IXFR",
		Tags:        []string{"Zone History"},
	}, func(ctx context.Context, in *histIn) (*struct{ Body map[string]any }, error) {
		if d.Client == nil {
			return nil, unavailable("DNS client not initialized")
		}
		zone := normalizeZone(in.Zone)
		if !d.Client.CheckZoneExists(ctx, zone) {
			return nil, notFound("Zone '" + zone + "' not found or not accessible")
		}
		fromSerial := uint32(1)
		if in.FromSerial >= 0 {
			fromSerial = uint32(in.FromSerial)
		} else {
			if cur, err := d.Client.QuerySOA(ctx, zone); err == nil {
				fromSerial = dnsx.OldestRequestableSerial(cur)
			}
		}
		hist, err := d.Client.GetZoneHistory(ctx, zone, fromSerial)
		if err != nil {
			return nil, mapDNSUpdateErr(err)
		}
		batches := make([]map[string]any, 0, len(hist.Batches))
		for _, b := range hist.Batches {
			changes := make([]map[string]any, 0, len(b.Changes))
			for _, c := range b.Changes {
				changes = append(changes, map[string]any{
					"action": c.Action, "name": c.Name, "ttl": c.TTL,
					"type": c.Type, "rdclass": c.RDClass, "records": c.Records,
				})
			}
			batches = append(batches, map[string]any{
				"from_serial": b.FromSerial, "to_serial": b.ToSerial, "changes": changes,
			})
		}
		return &struct{ Body map[string]any }{Body: map[string]any{
			"zone":                  hist.Zone,
			"current_serial":        hist.CurrentSerial,
			"history":               batches,
			"available_from_serial": hist.AvailableFromSerial,
			"is_full_axfr":          hist.IsFullAXFR,
		}}, nil
	})

	type previewIn struct {
		Zone         string `path:"zone"`
		TargetSerial uint32 `query:"target_serial" minimum:"1"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "rollback-preview",
		Method:      http.MethodGet,
		Path:        "/v1/zones/{zone}/history/rollback/preview",
		Summary:     "Preview zone rollback",
		Tags:        []string{"Zone History"},
	}, func(ctx context.Context, in *previewIn) (*struct{ Body map[string]any }, error) {
		if d.Client == nil {
			return nil, unavailable("DNS client not initialized")
		}
		zone := normalizeZone(in.Zone)
		if !d.Client.CheckZoneExists(ctx, zone) {
			return nil, notFound("Zone '" + zone + "' not found or not accessible")
		}
		current, err := d.Client.QuerySOA(ctx, zone)
		if err != nil {
			return nil, badGateway("Failed to get current zone serial")
		}
		base := map[string]any{
			"zone": zone, "current_serial": current, "target_serial": in.TargetSerial,
			"changes": []any{}, "change_count": 0, "can_rollback": false,
		}
		if !dnsx.Less(in.TargetSerial, current) {
			base["warning"] = "Target serial is not in the past (must be less than current serial per RFC 1982)"
			return &struct{ Body map[string]any }{Body: base}, nil
		}
		hist, err := d.Client.GetZoneHistory(ctx, zone, in.TargetSerial)
		if err != nil {
			return nil, mapDNSUpdateErr(err)
		}
		if hist.IsFullAXFR {
			base["warning"] = "History not available from target serial. Server responded with full zone transfer instead of incremental history."
			return &struct{ Body map[string]any }{Body: base}, nil
		}
		if dnsx.Less(in.TargetSerial, hist.AvailableFromSerial) && in.TargetSerial != hist.AvailableFromSerial {
			// Python uses integer < ; for serial wrap use Less when not equal
			if in.TargetSerial != hist.AvailableFromSerial && dnsx.Compare(in.TargetSerial, hist.AvailableFromSerial) < 0 {
				base["warning"] = "Target serial is older than available history"
				return &struct{ Body map[string]any }{Body: base}, nil
			}
		}
		// Match Python: target_serial < available_from_serial (unsigned modular via Less only when wrap matters;
		// for typical journals plain comparison is used in Python).
		if in.TargetSerial < hist.AvailableFromSerial {
			base["warning"] = "Target serial is older than available history"
			return &struct{ Body map[string]any }{Body: base}, nil
		}
		rev, warning := dnsx.ReverseHistoryChanges(hist.Batches, zone)
		changes := make([]map[string]any, 0, len(rev))
		for _, c := range rev {
			changes = append(changes, map[string]any{
				"action": c.Action, "name": c.Name, "ttl": c.TTL,
				"type": c.Type, "rdclass": c.RDClass, "records": c.Records,
			})
		}
		out := map[string]any{
			"zone": zone, "current_serial": current, "target_serial": in.TargetSerial,
			"changes": changes, "change_count": len(changes), "can_rollback": true,
		}
		if warning != "" {
			out["warning"] = warning
		}
		return &struct{ Body map[string]any }{Body: out}, nil
	})

	type rollbackIn struct {
		Zone string `path:"zone"`
		Body struct {
			TargetSerial uint32 `json:"target_serial" minimum:"1"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "rollback-zone",
		Method:      http.MethodPost,
		Path:        "/v1/zones/{zone}/history/rollback",
		Summary:     "Rollback zone to a previous serial",
		Tags:        []string{"Zone History"},
	}, func(ctx context.Context, in *rollbackIn) (*struct{ Body map[string]any }, error) {
		if d.Client == nil {
			return nil, unavailable("DNS client not initialized")
		}
		zone := normalizeZone(in.Zone)
		target := in.Body.TargetSerial
		if !d.Client.CheckZoneExists(ctx, zone) {
			return nil, notFound("Zone '" + zone + "' not found or not accessible")
		}
		current, err := d.Client.QuerySOA(ctx, zone)
		if err != nil {
			return nil, badGateway("Failed to get current zone serial")
		}
		if !dnsx.Less(target, current) {
			return nil, badRequest("Target serial must be less than current serial (per RFC 1982)")
		}
		hist, err := d.Client.GetZoneHistory(ctx, zone, target)
		if err != nil {
			return nil, mapDNSUpdateErr(err)
		}
		if hist.IsFullAXFR {
			return nil, badRequest("Cannot rollback: history not available from target serial")
		}
		if target < hist.AvailableFromSerial {
			return nil, badRequest("Cannot rollback: target serial older than available history")
		}
		rev, warning := dnsx.ReverseHistoryChanges(hist.Batches, zone)
		if len(rev) == 0 {
			return &struct{ Body map[string]any }{Body: map[string]any{
				"success": true, "zone": zone, "from_serial": current, "to_serial": target,
				"new_serial": current, "changes_applied": 0,
				"message": "No changes to apply (zone state unchanged or only protected records)",
			}}, nil
		}
		ops := make([]dnsx.Operation, 0, len(rev))
		for _, c := range rev {
			ops = append(ops, dnsx.Operation{
				Action: c.Action, Name: c.Name, Type: c.Type, Class: c.RDClass,
				TTL: c.TTL, Records: c.Records,
			})
		}
		built, err := dnsx.BuildUpdate(zone, ops, nil, false, false, nil, nil)
		if err != nil {
			return nil, mapDNSUpdateErr(err)
		}
		if _, err := d.Client.SendUpdate(ctx, zone, built.Msg); err != nil {
			if d.Cache != nil {
				_, _ = d.Cache.RefreshZone(ctx, zone, false)
			}
			return nil, mapDNSUpdateErr(err)
		}
		if d.Cache != nil {
			_, _ = d.Cache.RefreshZone(ctx, zone, false)
		}
		newSerial := current + 1
		if s, err := d.Client.QuerySOA(ctx, zone); err == nil {
			newSerial = s
		}
		msg := "Successfully rolled back zone to serial"
		if warning != "" {
			msg += " (" + warning + ")"
		}
		_ = dns.TypeSOA
		return &struct{ Body map[string]any }{Body: map[string]any{
			"success": true, "zone": zone, "from_serial": current, "to_serial": target,
			"new_serial": newSerial, "changes_applied": len(rev), "message": msg,
		}}, nil
	})
}
