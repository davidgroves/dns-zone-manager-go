package httpapi

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
	"github.com/davidgroves/dns-zone-manager-go/internal/metrics"
)

func registerSearch(api huma.API, d *Deps) {
	type zoneSearchIn struct {
		Zone         string `path:"zone"`
		NamePattern  string `query:"name_pattern"`
		ValuePattern string `query:"value_pattern"`
		Type         string `query:"type"`
		After        string `query:"after"`
		Limit        int    `query:"limit" default:"100" minimum:"1" maximum:"1000"`
		Offset       int    `query:"offset" minimum:"0"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "search-zone",
		Method:      http.MethodGet,
		Path:        "/v1/zones/{zone}/search",
		Summary:     "Search within a zone",
		Tags:        []string{"Search"},
	}, func(ctx context.Context, in *zoneSearchIn) (*struct{ Body PaginatedResults }, error) {
		if d.Cache == nil {
			return nil, unavailable("zone cache not initialized")
		}
		if in.NamePattern == "" && in.ValuePattern == "" {
			return nil, badRequest("name_pattern or value_pattern required")
		}
		nameRe, valueRe, err := compileSearchPatterns(in.NamePattern, in.ValuePattern)
		if err != nil {
			return nil, badRequest(err.Error())
		}
		zone := normalizeZone(in.Zone)
		var typ *uint16
		if in.Type != "" {
			t, err := dnsx.TypeFromString(in.Type)
			if err != nil {
				return nil, badRequest(err.Error())
			}
			typ = &t
		}
		offset := in.Offset
		var after *dnsx.NameCursor
		if in.After != "" {
			n, err := decodeNameCursor(in.After)
			if err != nil {
				return nil, badRequest("invalid after cursor")
			}
			after = &dnsx.NameCursor{Name: n}
		}
		items, total, next, hasMore, ok := d.Cache.SearchRRsets(zone, nameRe, valueRe, typ, offset, in.Limit, after)
		if !ok {
			return nil, notFound("Zone '" + zone + "' not found")
		}
		serial := uint32(0)
		if cz := d.Cache.PeekZone(zone); cz != nil {
			serial = cz.Serial
		}
		out := PaginatedResults{
			Zone: zone, Serial: serial,
			Results:    make([]RRsetResponse, 0, len(items)),
			TotalCount: total, HasMore: hasMore, PageSize: &in.Limit,
		}
		for _, it := range items {
			out.Results = append(out.Results, rrsetFromInfo(it))
		}
		if next != nil {
			c := encodeNameCursor(next.Name)
			out.NextCursor = &c
		}
		metrics.IncZoneSearches(zone)
		return &struct{ Body PaginatedResults }{Body: out}, nil
	})

	type globalIn struct {
		NamePattern  string `query:"name_pattern"`
		ValuePattern string `query:"value_pattern"`
		Type         string `query:"type"`
		After        string `query:"after"`
		Limit        int    `query:"limit" default:"100" minimum:"1" maximum:"1000"`
		Offset       int    `query:"offset" minimum:"0"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "search-global",
		Method:      http.MethodGet,
		Path:        "/v1/search",
		Summary:     "Search all cached zones",
		Tags:        []string{"Search"},
	}, func(ctx context.Context, in *globalIn) (*struct{ Body map[string]any }, error) {
		if d.Cache == nil {
			return nil, unavailable("zone cache not initialized")
		}
		if in.NamePattern == "" && in.ValuePattern == "" {
			return nil, badRequest("name_pattern or value_pattern required")
		}
		nameRe, valueRe, err := compileSearchPatterns(in.NamePattern, in.ValuePattern)
		if err != nil {
			return nil, badRequest(err.Error())
		}
		var typ *uint16
		if in.Type != "" {
			t, err := dnsx.TypeFromString(in.Type)
			if err != nil {
				return nil, badRequest(err.Error())
			}
			typ = &t
		}
		offset := in.Offset
		afterZone, afterName := "", ""
		if in.After != "" {
			afterZone, afterName, err = decodeGlobalCursor(in.After)
			if err != nil {
				return nil, badRequest("invalid after cursor")
			}
		}
		results, total, nextZone, nextName, hasMore := d.Cache.SearchAllZones(
			nameRe, valueRe, typ, offset, in.Limit, afterZone, afterName,
		)
		outResults := make([]map[string]any, 0, len(results))
		for _, zr := range results {
			idn := dnsx.GetIDNInfo(zr.Zone)
			rrsets := make([]RRsetResponse, 0, len(zr.RRsets))
			for _, r := range zr.RRsets {
				rrsets = append(rrsets, rrsetFromInfo(r))
			}
			item := map[string]any{
				"zone":        zr.Zone,
				"serial":      zr.Serial,
				"rrsets":      rrsets,
				"zone_is_idn": idn["has_idn"],
			}
			if u, ok := idn["unicode"].(string); ok && u != "" {
				item["zone_utf8"] = u
			}
			outResults = append(outResults, item)
		}
		body := map[string]any{
			"results":     outResults,
			"total_count": total,
			"has_more":    hasMore,
			"page_size":   in.Limit,
			"next_cursor": nil,
		}
		if hasMore && nextZone != "" {
			c := encodeGlobalCursor(nextZone, nextName)
			body["next_cursor"] = c
		}
		metrics.IncGlobalSearches()
		return &struct{ Body map[string]any }{Body: body}, nil
	})
}

func compileSearchPatterns(namePat, valuePat string) (*regexp.Regexp, *regexp.Regexp, error) {
	var nameRe, valueRe *regexp.Regexp
	var err error
	if namePat != "" {
		if err = validatePattern(namePat); err != nil {
			return nil, nil, err
		}
		nameRe, err = regexp.Compile("(?i)" + namePat)
		if err != nil {
			return nil, nil, err
		}
	}
	if valuePat != "" {
		if err = validatePattern(valuePat); err != nil {
			return nil, nil, err
		}
		valueRe, err = regexp.Compile("(?i)" + valuePat)
		if err != nil {
			return nil, nil, err
		}
	}
	return nameRe, valueRe, nil
}

func validatePattern(p string) error {
	if len(p) > 256 {
		return badRequest("pattern too long (max 256)")
	}
	// Reject nested quantifiers that can cause ReDoS.
	if strings.Contains(p, "++") || strings.Contains(p, "**") || strings.Contains(p, "}{") {
		return badRequest("pattern rejected")
	}
	return nil
}
