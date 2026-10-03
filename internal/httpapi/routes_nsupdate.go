package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/miekg/dns"

	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
	"github.com/davidgroves/dns-zone-manager-go/internal/store"
)

func registerNSUpdate(api huma.API, mux *http.ServeMux, d *Deps) {
	mux.HandleFunc("POST /v1/nsupdate", func(w http.ResponseWriter, r *http.Request) {
		handleNSUpdate(w, r, d)
	})
	mux.HandleFunc("POST /v1/nsupdate/drafts", func(w http.ResponseWriter, r *http.Request) {
		handleNSUpdateDrafts(w, r, d)
	})

	type humaIn struct {
		Zone    string `query:"zone"`
		DryRun  bool   `query:"dry_run"`
		RawBody []byte
	}
	_ = api
	_ = humaIn{}
}

func handleNSUpdate(w http.ResponseWriter, r *http.Request, d *Deps) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	dryRun := r.URL.Query().Get("dry_run") == "true" || r.URL.Query().Get("dry_run") == "1"
	zone := r.URL.Query().Get("zone")
	out, err := runNSUpdate(r.Context(), d, string(body), zone, dryRun)
	if err != nil {
		writeHandlerError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func handleNSUpdateDrafts(w http.ResponseWriter, r *http.Request, d *Deps) {
	if d.Store == nil {
		http.Error(w, `{"detail":"scheduler store not available"}`, http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := enforceNSUpdateLimits(d, string(body)); err != nil {
		writeHandlerError(w, err)
		return
	}
	zone := r.URL.Query().Get("zone")
	drafts, err := dnsx.NSUpdateTextToCreates(string(body), zone, time.Now().UTC())
	if err != nil {
		writeHandlerError(w, badRequest(err.Error()))
		return
	}
	u, _ := userFrom(r.Context())
	created := make([]map[string]any, 0, len(drafts))
	for _, dr := range drafts {
		ops := make([]store.AtomicOperation, 0, len(dr.Operations))
		for _, op := range dr.Operations {
			ops = append(ops, store.AtomicOperation{
				Action: op.Action, Name: op.Name, Type: op.Type,
				RDClass: op.Class, TTL: op.TTL, Records: op.Records,
			})
		}
		prereqs := make([]store.ChangePrerequisite, 0, len(dr.Prerequisites))
		for _, p := range dr.Prerequisites {
			cp := store.ChangePrerequisite{PrereqType: p.Type, Name: p.Name, RDClass: p.Class}
			if p.RdType != "" {
				rd := p.RdType
				cp.RDType = &rd
			}
			if p.Data != "" {
				data := p.Data
				cp.Data = &data
			}
			prereqs = append(prereqs, cp)
		}
		ch, err := d.Store.Create(r.Context(), store.ChangeCreateData{
			Name: dr.Name, Description: strPtr(dr.Description), Zone: dr.Zone,
			Operations: ops, Prerequisites: prereqs,
			AutoPrerequisites: dr.AutoPrerequisites, CreatedBy: actorPtr(u.ID),
		})
		if err != nil {
			writeHandlerError(w, internalErr(err.Error()))
			return
		}
		created = append(created, scheduledChangeResponse(ch))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"created": created, "total": len(created)})
}

func runNSUpdate(ctx context.Context, d *Deps, text, defaultZone string, dryRun bool) (map[string]any, error) {
	if d.Client == nil {
		return nil, unavailable("DNS client not initialized")
	}
	if err := enforceNSUpdateLimits(d, text); err != nil {
		return nil, err
	}
	parsed, err := dnsx.ParseNSUpdate(text, defaultZone)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	if len(parsed) > d.Settings.NSUpdate.MaxTransactions {
		return nil, badRequest("Too many transactions")
	}

	results := make([]map[string]any, 0, len(parsed))
	success, failed := 0, 0
	for _, txn := range parsed {
		zone := normalizeZone(txn.Zone)
		opsOut := make([]map[string]any, 0, len(txn.Operations))
		for _, op := range txn.Operations {
			entry := map[string]any{"action": op.Action, "name": op.Name, "rdtype": op.RdType, "data": op.Data}
			if op.TTL != nil {
				entry["ttl"] = *op.TTL
			}
			opsOut = append(opsOut, entry)
		}
		tr := map[string]any{
			"zone": zone, "operations": opsOut, "success": true, "message": "ok",
		}
		if dryRun {
			tr["message"] = "dry_run: not applied"
			tr["dry_run"] = true
			success++
			results = append(results, tr)
			continue
		}

		dnsOps := make([]dnsx.Operation, 0, len(txn.Operations))
		for _, op := range txn.Operations {
			ttl := uint32(0)
			if op.TTL != nil {
				ttl = *op.TTL
			}
			recs := []string{}
			if op.Data != "" {
				recs = []string{op.Data}
			}
			dnsOps = append(dnsOps, dnsx.Operation{
				Action: op.Action, Name: op.Name, Type: op.RdType,
				Class: "IN", TTL: ttl, Records: recs,
			})
		}
		prereqs := make([]dnsx.Prerequisite, 0, len(txn.Prerequisites))
		for _, p := range txn.Prerequisites {
			prereqs = append(prereqs, dnsx.Prerequisite{
				Type: p.PrereqType, Name: p.Name, RdType: p.RdType, Class: "IN", Data: p.Data,
			})
		}
		lookup := func(name, typ string) (uint32, []string, bool) {
			if d.Cache == nil {
				return 0, nil, false
			}
			cz := d.Cache.PeekZone(zone)
			if cz == nil || cz.Zone == nil {
				return 0, nil, false
			}
			tc, err := dnsx.TypeFromString(typ)
			if err != nil {
				return 0, nil, false
			}
			info, ok := cz.Zone.GetRRset(name, tc)
			if !ok {
				return 0, nil, false
			}
			return info.TTL, info.Records, true
		}
		typesAt := func(name string) []string {
			if d.Cache == nil {
				return nil
			}
			cz := d.Cache.PeekZone(zone)
			if cz == nil || cz.Zone == nil {
				return nil
			}
			return cz.Zone.TypesAtName(name)
		}
		built, err := dnsx.BuildUpdate(zone, dnsOps, prereqs, true, false, lookup, typesAt)
		if err != nil {
			tr["success"] = false
			tr["message"] = err.Error()
			failed++
			results = append(results, tr)
			continue
		}
		resp, err := d.Client.SendUpdate(ctx, zone, built.Msg)
		if err != nil {
			tr["success"] = false
			tr["message"] = err.Error()
			var pe *dnsx.PrerequisiteError
			if asPrerequisite(err, &pe) {
				tr["rcode"] = pe.RcodeText
				tr["rcode_description"] = dnsx.RcodeDescription(pe.RcodeText)
			} else if resp != nil {
				rc := dns.RcodeToString[resp.Rcode]
				tr["rcode"] = rc
				tr["rcode_description"] = dnsx.RcodeDescription(rc)
			}
			failed++
		} else {
			success++
			if d.Cache != nil {
				if cz := d.Cache.PeekZone(zone); cz != nil && cz.Zone != nil {
					_ = dnsx.ApplyCacheUpdates(cz.Zone, built.CacheUpdates)
				}
			}
		}
		results = append(results, tr)
	}
	return map[string]any{
		"transactions":  results,
		"total_success": success,
		"total_failed":  failed,
		"dry_run":       dryRun,
	}, nil
}

func asPrerequisite(err error, target **dnsx.PrerequisiteError) bool {
	var pe *dnsx.PrerequisiteError
	if errors.As(err, &pe) {
		*target = pe
		return true
	}
	return false
}

func enforceNSUpdateLimits(d *Deps, text string) error {
	limits := d.Settings.NSUpdate
	if int64(len(text)) > limits.MaxBodyBytes {
		return badRequest("Request body exceeds limit")
	}
	lines := strings.Count(text, "\n") + 1
	if strings.HasSuffix(text, "\n") {
		lines--
	}
	if lines > limits.MaxLines {
		return badRequest("Request body exceeds line limit")
	}
	return nil
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func writeHandlerError(w http.ResponseWriter, err error) {
	if se, ok := err.(interface{ GetStatus() int }); ok {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(se.GetStatus())
		_ = json.NewEncoder(w).Encode(err)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
