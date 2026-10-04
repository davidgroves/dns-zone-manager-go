package provision

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/davidgroves/rndc-go"
	"github.com/miekg/dns"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
	"github.com/davidgroves/dns-zone-manager-go/internal/logging"
	"github.com/davidgroves/dns-zone-manager-go/internal/store"
)

// CatalogSource is the optional catalog indexer used to discover PTR owners.
type CatalogSource interface {
	ZoneName() string
	PeekZone() *dnsx.Zone
	Refresh(ctx context.Context) error
}

// CreateRequest is the input for creating a zone via rndc addzone.
type CreateRequest struct {
	Zone        string     `json:"zone"`
	PrimaryNS   string     `json:"primary_ns,omitempty"`
	AdminEmail  string     `json:"admin_email,omitempty"`
	Nameservers []string   `json:"nameservers,omitempty"`
	TTL         *uint32    `json:"ttl,omitempty"`
	SOA         *SOAParams `json:"soa,omitempty"`
	Catalog     *bool      `json:"catalog,omitempty"`
}

// DeleteOptions controls rndc delzone and catalog membership removal.
type DeleteOptions struct {
	KeepFiles bool  `json:"keep_files,omitempty"`
	Catalog   *bool `json:"catalog,omitempty"`
}

// Result is returned after a successful create or delete.
type Result struct {
	Zone           string  `json:"zone"`
	Serial         *uint32 `json:"serial,omitempty"`
	CatalogAdded   bool    `json:"catalog_added,omitempty"`
	CatalogRemoved bool    `json:"catalog_removed,omitempty"`
	SeedMode       string  `json:"seed_mode,omitempty"`
}

// Status is the RNDC capabilities payload for GET /v1/rndc/status.
type Status struct {
	Enabled        bool                    `json:"enabled"`
	Connected      bool                    `json:"connected"`
	Host           string                  `json:"host,omitempty"`
	Port           int                     `json:"port,omitempty"`
	SeedMode       string                  `json:"seed_mode,omitempty"`
	CatalogEnabled bool                    `json:"catalog_enabled"`
	Defaults       config.RNDCZoneDefaults `json:"defaults,omitempty"`
}

// Provisioner creates and deletes BIND zones via rndc and catalog DDNS.
type Provisioner struct {
	settings *config.Settings
	client   *dnsx.Client
	cache    *dnsx.ZoneCache
	catalog  CatalogSource
	store    *store.Store
	log      *slog.Logger
	dial     func(ctx context.Context, cfg rndc.Config) (*rndc.Client, error)
}

func New(settings *config.Settings, client *dnsx.Client, cache *dnsx.ZoneCache, catalog CatalogSource, st *store.Store) *Provisioner {
	return &Provisioner{
		settings: settings,
		client:   client,
		cache:    cache,
		catalog:  catalog,
		store:    st,
		log:      logging.Default(),
		dial:     rndc.Dial,
	}
}

func (p *Provisioner) enabled() bool {
	return p != nil && p.settings != nil && p.settings.RNDC.Enabled
}

func (p *Provisioner) Status(ctx context.Context) Status {
	st := Status{}
	if p == nil || p.settings == nil {
		return st
	}
	r := p.settings.RNDC
	st.Enabled = r.Enabled
	st.Host = r.Host
	st.Port = r.Port
	st.SeedMode = r.ZoneSeed.Mode
	st.CatalogEnabled = p.settings.Catalog.Enabled
	st.Defaults = r.ZoneDefaults
	if !r.Enabled {
		return st
	}
	err := p.withClient(ctx, func(c *rndc.Client) error {
		_, err := c.Status(ctx)
		return err
	})
	st.Connected = err == nil
	return st
}

func (p *Provisioner) CreateZone(ctx context.Context, req CreateRequest) (Result, error) {
	if !p.enabled() {
		return Result{}, ErrDisabled
	}
	zone := dnsx.NormalizeZoneName(req.Zone)
	if err := dnsx.ValidateZoneName(zone); err != nil {
		return Result{}, err
	}
	req.Zone = zone

	if p.cache != nil {
		if cz := p.cache.PeekZone(zone); cz != nil {
			return Result{}, fmt.Errorf("%w: %s", ErrZoneExists, zone)
		}
	}
	if p.client != nil {
		if _, err := p.client.QuerySOA(ctx, zone); err == nil {
			return Result{}, fmt.Errorf("%w: %s", ErrZoneExists, zone)
		}
	}

	cfg := p.settings.RNDC
	seedMode := strings.ToLower(cfg.ZoneSeed.Mode)
	if seedMode == "" {
		seedMode = config.RNDCSeedSharedDir
	}
	if seedMode == config.RNDCSeedSharedDir {
		text := renderSeedZone(zone, cfg.ZoneDefaults, req, 0)
		path := localZonePath(cfg.ZoneSeed.LocalDir, zone)
		if err := os.MkdirAll(cfg.ZoneSeed.LocalDir, 0o777); err != nil {
			return Result{}, fmt.Errorf("create zone directory: %w", err)
		}
		if err := os.WriteFile(path, []byte(text), 0o666); err != nil {
			return Result{}, fmt.Errorf("write seed zone file: %w", err)
		}
	}

	addCfg := renderAddzoneConfig(zone, cfg)
	name := strings.TrimSuffix(zone, ".")
	cmd := "addzone " + name + " " + addCfg + ";"
	if view := strings.TrimSpace(cfg.View); view != "" {
		cmd = "addzone " + name + " IN " + view + " " + addCfg + ";"
	}
	err := p.withClient(ctx, func(c *rndc.Client) error {
		resp, err := c.Call(ctx, cmd)
		if err != nil {
			return err
		}
		if resp.ResultCode() == 16 || strings.Contains(strings.ToLower(resp.Err()), "already exists") {
			return fmt.Errorf("%w: zone %s already exists", rndc.ErrZoneAlreadyExists, zone)
		}
		if resp.ResultCode() != 0 {
			msg := resp.Err()
			if msg == "" {
				msg = resp.Text()
			}
			if msg == "" {
				msg = fmt.Sprintf("result %d", resp.ResultCode())
			}
			return fmt.Errorf("%s", msg)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, rndc.ErrZoneAlreadyExists) {
			return Result{}, fmt.Errorf("%w: %s", ErrZoneExists, zone)
		}
		return Result{}, fmt.Errorf("rndc addzone: %w", err)
	}

	serial, err := p.waitReady(ctx, zone, cfg.ReadyTimeout)
	if err != nil {
		return Result{}, err
	}

	addCatalog := true
	if req.Catalog != nil {
		addCatalog = *req.Catalog
	}
	catalogAdded := false
	if addCatalog {
		added, catErr := p.addCatalogMember(ctx, zone)
		if catErr != nil {
			logging.LogInternalEvent(p.log, "provision_catalog_add_failed", slog.LevelWarn,
				slog.String("zone", zone), slog.String("error", catErr.Error()))
		}
		catalogAdded = added
	}

	if p.cache != nil {
		if _, loadErr := p.cache.LoadZone(ctx, zone); loadErr != nil {
			logging.LogInternalEvent(p.log, "provision_cache_load_failed", slog.LevelWarn,
				slog.String("zone", zone), slog.String("error", loadErr.Error()))
		}
	}

	res := Result{Zone: zone, Serial: &serial, CatalogAdded: catalogAdded, SeedMode: seedMode}
	p.recordAudit(ctx, "zone_create", zone, true, nil)
	return res, nil
}

func (p *Provisioner) DeleteZone(ctx context.Context, zone string, opts DeleteOptions) (Result, error) {
	if !p.enabled() {
		return Result{}, ErrDisabled
	}
	zone = dnsx.NormalizeZoneName(zone)
	if err := dnsx.ValidateZoneName(zone); err != nil {
		return Result{}, err
	}

	removeCatalog := true
	if opts.Catalog != nil {
		removeCatalog = *opts.Catalog
	}
	catalogRemoved := false
	if removeCatalog {
		removed, catErr := p.removeCatalogMember(ctx, zone)
		if catErr != nil {
			logging.LogInternalEvent(p.log, "provision_catalog_remove_failed", slog.LevelWarn,
				slog.String("zone", zone), slog.String("error", catErr.Error()))
		}
		catalogRemoved = removed
	}

	var soaErr error
	if p.client != nil {
		_, soaErr = p.client.QuerySOA(ctx, zone)
	}

	delOpts := []rndc.ZoneOption{}
	if !opts.KeepFiles {
		delOpts = append(delOpts, rndc.WithClean())
	}
	if view := strings.TrimSpace(p.settings.RNDC.View); view != "" {
		delOpts = append(delOpts, rndc.WithView(view))
	}
	err := p.withClient(ctx, func(c *rndc.Client) error {
		return c.DelZone(ctx, zone, delOpts...)
	})
	if err != nil {
		if errors.Is(err, rndc.ErrZoneNotFound) {
			return Result{}, fmt.Errorf("%w: %s", ErrZoneNotFound, zone)
		}
		return Result{}, fmt.Errorf("rndc delzone: %w", err)
	}
	if soaErr != nil {
		return Result{}, fmt.Errorf("%w: %s", ErrZoneNotFound, zone)
	}

	if p.cache != nil {
		p.cache.Invalidate(zone)
	}

	res := Result{Zone: zone, CatalogRemoved: catalogRemoved}
	p.recordAudit(ctx, "zone_delete", zone, true, nil)
	return res, nil
}

func (p *Provisioner) waitReady(ctx context.Context, zone string, timeout time.Duration) (uint32, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if p.client == nil {
			return 0, fmt.Errorf("%w: dns client missing", ErrNotReady)
		}
		serial, err := p.client.QuerySOA(ctx, zone)
		if err == nil {
			return serial, nil
		}
		last = err
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	if last == nil {
		last = errors.New("timeout")
	}
	return 0, fmt.Errorf("%w: %s: %w", ErrNotReady, zone, last)
}

func (p *Provisioner) wantCatalog() bool {
	return p.settings != nil && p.settings.Catalog.Enabled && strings.TrimSpace(p.settings.Catalog.ZoneName) != ""
}

func (p *Provisioner) catalogZoneName() string {
	if p.catalog != nil && p.catalog.ZoneName() != "" {
		return dnsx.NormalizeZoneName(p.catalog.ZoneName())
	}
	return dnsx.NormalizeZoneName(p.settings.Catalog.ZoneName)
}

func (p *Provisioner) catalogZone(ctx context.Context) *dnsx.Zone {
	if p.catalog != nil {
		if z := p.catalog.PeekZone(); z != nil {
			return z
		}
	}
	if p.client == nil {
		return nil
	}
	z, err := p.client.PerformAXFR(ctx, p.catalogZoneName())
	if err != nil {
		return nil
	}
	return z
}

func (p *Provisioner) addCatalogMember(ctx context.Context, zone string) (bool, error) {
	if !p.wantCatalog() {
		logging.LogInternalEvent(p.log, "provision_catalog_skipped", slog.LevelInfo,
			slog.String("zone", zone), slog.String("reason", "catalog disabled"))
		return false, nil
	}
	if p.client == nil {
		return false, errors.New("dns client missing")
	}
	cat := p.catalogZoneName()
	label := memberLabel(zone)
	ttl := p.settings.RNDC.CatalogMemberTTL
	if ttl == 0 {
		ttl = 3600
	}
	owner := label + ".zones"
	msg, err := p.client.PrepareAdd(cat, owner, ttl, "PTR", "IN", []string{zone}, true)
	if err != nil {
		return false, err
	}
	_, err = p.client.SendUpdate(ctx, cat, msg)
	if err != nil {
		var pre *dnsx.PrerequisiteError
		if errors.As(err, &pre) && strings.EqualFold(pre.RcodeText, "YXRRSET") {
			return false, nil
		}
		return false, err
	}
	if p.catalog != nil {
		_ = p.catalog.Refresh(ctx)
	}
	return true, nil
}

func (p *Provisioner) removeCatalogMember(ctx context.Context, zone string) (bool, error) {
	if !p.wantCatalog() {
		return false, nil
	}
	if p.client == nil {
		return false, errors.New("dns client missing")
	}
	cat := p.catalogZoneName()
	owners := catalogPTROwners(p.catalogZone(ctx), cat, zone)
	if len(owners) == 0 {
		owners = []string{memberLabel(zone) + ".zones." + cat}
	}
	removed := false
	for _, owner := range owners {
		msg, err := p.client.PrepareDelete(cat, owner, "PTR", "IN", nil, nil)
		if err != nil {
			return removed, err
		}
		_, err = p.client.SendUpdate(ctx, cat, msg)
		if err != nil {
			var pre *dnsx.PrerequisiteError
			if errors.As(err, &pre) {
				continue
			}
			return removed, err
		}
		removed = true
	}
	if p.catalog != nil {
		_ = p.catalog.Refresh(ctx)
	}
	return removed, nil
}

func catalogPTROwners(z *dnsx.Zone, catalogZone, member string) []string {
	if z == nil {
		return nil
	}
	member = dnsx.NormalizeZoneName(member)
	catalogZone = dnsx.NormalizeZoneName(catalogZone)
	suffix := "zones." + catalogZone
	var owners []string
	for _, rr := range z.AllRRs() {
		ptr, ok := rr.(*dns.PTR)
		if !ok {
			continue
		}
		if dnsx.NormalizeZoneName(ptr.Ptr) != member {
			continue
		}
		name := strings.ToLower(dns.Fqdn(ptr.Hdr.Name))
		if !strings.HasSuffix(name, "."+suffix) && name != suffix {
			continue
		}
		owners = append(owners, name)
	}
	return owners
}

func (p *Provisioner) recordAudit(ctx context.Context, kind, zone string, ok bool, err error) {
	if p.store == nil {
		return
	}
	status := store.StatusApplied
	var errStr *string
	rcode := "NOERROR"
	if !ok {
		status = store.StatusFailed
		if err != nil {
			s := err.Error()
			errStr = &s
		}
		rcode = "ERROR"
	}
	name := kind + " " + strings.TrimSuffix(zone, ".")
	_, _ = p.store.RecordExternalChange(ctx, store.RecordExternalOpts{
		Name:        name,
		Zone:        zone,
		Kind:        kind,
		Status:      status,
		Trigger:     "provision",
		Source:      "manual",
		ResultRcode: &rcode,
		Error:       errStr,
	})
}

func (p *Provisioner) withClient(ctx context.Context, fn func(*rndc.Client) error) error {
	cfg, err := p.rndcConfig()
	if err != nil {
		return err
	}
	dial := p.dial
	if dial == nil {
		dial = rndc.Dial
	}
	c, err := dial(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	return fn(c)
}

func (p *Provisioner) rndcConfig() (rndc.Config, error) {
	r := p.settings.RNDC
	alg, err := rndc.ParseAlgorithm(r.Algorithm)
	if err != nil {
		return rndc.Config{}, fmt.Errorf("rndc algorithm: %w", err)
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return rndc.Config{
		Host:      r.Host,
		Port:      r.Port,
		Algorithm: alg,
		Secret:    r.Secret.String(),
		Timeout:   timeout,
	}, nil
}
