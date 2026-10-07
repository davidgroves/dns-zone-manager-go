package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
	"github.com/davidgroves/dns-zone-manager-go/internal/provision"
	"github.com/davidgroves/dns-zone-manager-go/internal/store"
)

func registerProvision(api huma.API, d *Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "rndc-status",
		Method:      http.MethodGet,
		Path:        "/v1/rndc/status",
		Summary:     "RNDC / zone-provisioning status",
		Tags:        []string{"Zones"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body provision.Status }, error) {
		if d.Provisioner == nil {
			return &struct{ Body provision.Status }{Body: provision.Status{Enabled: false}}, nil
		}
		return &struct{ Body provision.Status }{Body: d.Provisioner.Status(ctx)}, nil
	})

	type soaBody struct {
		Refresh uint32 `json:"refresh,omitempty"`
		Retry   uint32 `json:"retry,omitempty"`
		Expire  uint32 `json:"expire,omitempty"`
		Minimum uint32 `json:"minimum,omitempty"`
	}
	type createIn struct {
		Body struct {
			Zone          string     `json:"zone"`
			PrimaryNS     string     `json:"primary_ns,omitempty"`
			AdminEmail    string     `json:"admin_email,omitempty"`
			Nameservers   []string   `json:"nameservers,omitempty"`
			TTL           *uint32    `json:"ttl,omitempty"`
			SOA           *soaBody   `json:"soa,omitempty"`
			Catalog       *bool      `json:"catalog,omitempty"`
			ScheduledAt   *time.Time `json:"scheduled_at,omitempty"`
			NotValidAfter *time.Time `json:"not_valid_after,omitempty"`
			Name          string     `json:"name,omitempty"`
			Description   *string    `json:"description,omitempty"`
		}
	}
	type statusBody struct {
		Status int
		Body   map[string]any
	}
	huma.Register(api, huma.Operation{
		OperationID:   "create-zone",
		Method:        http.MethodPost,
		Path:          "/v1/zones",
		Summary:       "Create a zone via rndc addzone",
		Tags:          []string{"Zones"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createIn) (*statusBody, error) {
		if d.Provisioner == nil {
			return nil, rndcNotConfigured()
		}
		req := provision.CreateRequest{
			Zone:        in.Body.Zone,
			PrimaryNS:   in.Body.PrimaryNS,
			AdminEmail:  in.Body.AdminEmail,
			Nameservers: in.Body.Nameservers,
			TTL:         in.Body.TTL,
			Catalog:     in.Body.Catalog,
		}
		if in.Body.SOA != nil {
			req.SOA = &provision.SOAParams{
				Refresh: in.Body.SOA.Refresh,
				Retry:   in.Body.SOA.Retry,
				Expire:  in.Body.SOA.Expire,
				Minimum: in.Body.SOA.Minimum,
			}
		}
		if in.Body.ScheduledAt != nil {
			ch, err := scheduleZoneChange(ctx, d, store.KindZoneCreate, req.Zone, in.Body.Name, in.Body.Description, in.Body.ScheduledAt, in.Body.NotValidAfter, req)
			if err != nil {
				return nil, err
			}
			return &statusBody{Status: http.StatusAccepted, Body: scheduledChangeResponse(ch)}, nil
		}
		res, err := d.Provisioner.CreateZone(ctx, req)
		if err != nil {
			return nil, mapProvisionErr(err)
		}
		return &statusBody{Status: http.StatusCreated, Body: provisionResultMap(res)}, nil
	})

	type deleteIn struct {
		Zone          string `path:"zone"`
		KeepFiles     bool   `query:"keep_files"`
		Catalog       string `query:"catalog"`
		ScheduledAt   string `query:"scheduled_at"`
		NotValidAfter string `query:"not_valid_after"`
		Name          string `query:"name"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "delete-zone",
		Method:      http.MethodDelete,
		Path:        "/v1/zones/{zone}",
		Summary:     "Delete a zone via rndc delzone",
		Tags:        []string{"Zones"},
	}, func(ctx context.Context, in *deleteIn) (*statusBody, error) {
		if d.Provisioner == nil {
			return nil, rndcNotConfigured()
		}
		opts := provision.DeleteOptions{KeepFiles: in.KeepFiles}
		if in.Catalog != "" {
			v := strings.EqualFold(in.Catalog, "true")
			opts.Catalog = &v
		}
		zone := normalizeZone(in.Zone)
		if in.ScheduledAt != "" {
			at, err := time.Parse(time.RFC3339Nano, in.ScheduledAt)
			if err != nil {
				at, err = time.Parse(time.RFC3339, in.ScheduledAt)
			}
			if err != nil {
				return nil, badRequest("invalid scheduled_at")
			}
			var nva *time.Time
			if in.NotValidAfter != "" {
				t, err := time.Parse(time.RFC3339Nano, in.NotValidAfter)
				if err != nil {
					t, err = time.Parse(time.RFC3339, in.NotValidAfter)
				}
				if err != nil {
					return nil, badRequest("invalid not_valid_after")
				}
				nva = &t
			}
			ch, err := scheduleZoneChange(ctx, d, store.KindZoneDelete, zone, in.Name, nil, &at, nva, opts)
			if err != nil {
				return nil, err
			}
			return &statusBody{Status: http.StatusAccepted, Body: scheduledChangeResponse(ch)}, nil
		}
		res, err := d.Provisioner.DeleteZone(ctx, zone, opts)
		if err != nil {
			return nil, mapProvisionErr(err)
		}
		return &statusBody{Status: http.StatusOK, Body: provisionResultMap(res)}, nil
	})

	type catalogMemberIn struct {
		Zone          string `path:"zone"`
		ScheduledAt   string `query:"scheduled_at"`
		NotValidAfter string `query:"not_valid_after"`
		Name          string `query:"name"`
	}
	parseCatalogSchedule := func(in *catalogMemberIn) (at *time.Time, nva *time.Time, err error) {
		if in.ScheduledAt == "" {
			return nil, nil, nil
		}
		t, perr := time.Parse(time.RFC3339Nano, in.ScheduledAt)
		if perr != nil {
			t, perr = time.Parse(time.RFC3339, in.ScheduledAt)
		}
		if perr != nil {
			return nil, nil, badRequest("invalid scheduled_at")
		}
		at = &t
		if in.NotValidAfter != "" {
			nv, perr := time.Parse(time.RFC3339Nano, in.NotValidAfter)
			if perr != nil {
				nv, perr = time.Parse(time.RFC3339, in.NotValidAfter)
			}
			if perr != nil {
				return nil, nil, badRequest("invalid not_valid_after")
			}
			nva = &nv
		}
		return at, nva, nil
	}
	huma.Register(api, huma.Operation{
		OperationID: "add-zone-to-catalog",
		Method:      http.MethodPut,
		Path:        "/v1/zones/{zone}/catalog",
		Summary:     "Publish a zone to the catalog",
		Tags:        []string{"Zones"},
	}, func(ctx context.Context, in *catalogMemberIn) (*statusBody, error) {
		if d.Provisioner == nil {
			return nil, rndcNotConfigured()
		}
		zone := normalizeZone(in.Zone)
		at, nva, err := parseCatalogSchedule(in)
		if err != nil {
			return nil, err
		}
		if at != nil {
			ch, err := scheduleZoneChange(ctx, d, store.KindZoneCatalogAdd, zone, in.Name, nil, at, nva, map[string]any{"zone": zone})
			if err != nil {
				return nil, err
			}
			return &statusBody{Status: http.StatusAccepted, Body: scheduledChangeResponse(ch)}, nil
		}
		res, err := d.Provisioner.AddToCatalog(ctx, zone)
		if err != nil {
			return nil, mapProvisionErr(err)
		}
		return &statusBody{Status: http.StatusOK, Body: provisionResultMap(res)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "remove-zone-from-catalog",
		Method:      http.MethodDelete,
		Path:        "/v1/zones/{zone}/catalog",
		Summary:     "Remove a zone from the catalog (keep on primary)",
		Tags:        []string{"Zones"},
	}, func(ctx context.Context, in *catalogMemberIn) (*statusBody, error) {
		if d.Provisioner == nil {
			return nil, rndcNotConfigured()
		}
		zone := normalizeZone(in.Zone)
		at, nva, err := parseCatalogSchedule(in)
		if err != nil {
			return nil, err
		}
		if at != nil {
			ch, err := scheduleZoneChange(ctx, d, store.KindZoneCatalogRemove, zone, in.Name, nil, at, nva, map[string]any{"zone": zone})
			if err != nil {
				return nil, err
			}
			return &statusBody{Status: http.StatusAccepted, Body: scheduledChangeResponse(ch)}, nil
		}
		res, err := d.Provisioner.RemoveFromCatalog(ctx, zone)
		if err != nil {
			return nil, mapProvisionErr(err)
		}
		return &statusBody{Status: http.StatusOK, Body: provisionResultMap(res)}, nil
	})
}

func scheduleZoneChange(ctx context.Context, d *Deps, kind, zone, name string, desc *string, at, nva *time.Time, payload any) (*store.ScheduledChange, error) {
	if d.Store == nil {
		return nil, unavailable("scheduler store not available")
	}
	zone = normalizeZone(zone)
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, internalErr(err.Error())
	}
	if strings.TrimSpace(name) == "" {
		name = kind + " " + strings.TrimSuffix(zone, ".")
	}
	u, _ := userFrom(ctx)
	ch, err := d.Store.Create(ctx, store.ChangeCreateData{
		Name: name, Description: desc, Zone: zone,
		Kind: kind, Payload: raw,
		ScheduledAt: at, NotValidAfter: nva,
		CreatedBy: actorPtr(u.ID),
	})
	if err != nil {
		return nil, badRequest(err.Error())
	}
	return ch, nil
}

func provisionResultMap(res provision.Result) map[string]any {
	out := map[string]any{"zone": res.Zone}
	if res.Serial != nil {
		out["serial"] = *res.Serial
	}
	if res.CatalogAdded {
		out["catalog_added"] = true
	}
	if res.CatalogRemoved {
		out["catalog_removed"] = true
	}
	if res.SeedMode != "" {
		out["seed_mode"] = res.SeedMode
	}
	return out
}

func rndcNotConfigured() error {
	return problem(http.StatusNotImplemented, "rndc_not_configured", "Not Implemented", "RNDC is not configured")
}

func mapProvisionErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, provision.ErrDisabled) {
		return rndcNotConfigured()
	}
	if errors.Is(err, provision.ErrCatalogDisabled) {
		return problem(http.StatusNotImplemented, "catalog_not_configured", "Not Implemented", "Catalog is not configured")
	}
	if errors.Is(err, provision.ErrZoneExists) {
		return conflict(err.Error())
	}
	if errors.Is(err, provision.ErrZoneNotFound) {
		return notFound(err.Error())
	}
	if errors.Is(err, provision.ErrNotReady) {
		return unavailable(err.Error())
	}
	var zn *dnsx.ZoneNameError
	if errors.As(err, &zn) {
		return badRequest(zn.Error())
	}
	return mapDNSUpdateErr(err)
}
