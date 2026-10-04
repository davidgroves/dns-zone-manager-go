package catalog

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
	"github.com/davidgroves/dns-zone-manager-go/internal/logging"
)

// Config configures a catalog zone Indexer.
type Config struct {
	ZoneName          string
	PollInterval      time.Duration
	NotifyBindAddress string
	NotifyUDPPort     int
	NotifyTCPPort     int
	// Enabled is reported by Status(); Start may still be called when true.
	Enabled bool
}

// ConfigFromSettings builds catalog indexer config from app settings.
func ConfigFromSettings(s *config.Settings) Config {
	if s == nil {
		return Config{}
	}
	poll := time.Duration(s.Catalog.PollInterval * float64(time.Second))
	if poll <= 0 {
		poll = 5 * time.Minute
	}
	return Config{
		Enabled:           s.Catalog.Enabled,
		ZoneName:          dnsx.NormalizeZoneName(s.Catalog.ZoneName),
		PollInterval:      poll,
		NotifyBindAddress: s.Catalog.NotifyBindAddress,
		NotifyUDPPort:     s.Catalog.NotifyUDPPort,
		NotifyTCPPort:     s.Catalog.NotifyTCPPort,
	}
}

// Status describes the current indexer state for /v1/catalog/status.
type Status struct {
	Enabled         bool    `json:"enabled"`
	Connected       bool    `json:"connected"`
	ZoneName        string  `json:"zone_name"`
	Serial          *uint32 `json:"serial"`
	ZonesDiscovered int     `json:"zones_discovered"`
	PollInterval    float64 `json:"poll_interval"`
	Running         bool    `json:"running"`
}

// Indexer AXFRs a catalog zone periodically (and on NOTIFY), and exposes member zones.
type Indexer struct {
	cfg     Config
	backend dnsx.TransferBackend
	log     *slog.Logger

	mu             sync.RWMutex
	zone           *dnsx.Zone
	serial         *uint32
	members        []string
	running        bool
	effectivePoll  time.Duration
	cancel         context.CancelFunc
	notifyListener *dnsx.NotifyListener
	refreshMu      sync.Mutex
	wg             sync.WaitGroup
}

// New creates an Indexer that uses backend for AXFR / SOA queries.
func New(cfg Config, backend dnsx.TransferBackend) *Indexer {
	cfg.ZoneName = dnsx.NormalizeZoneName(cfg.ZoneName)
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Minute
	}
	if cfg.NotifyBindAddress == "" {
		cfg.NotifyBindAddress = "0.0.0.0"
	}
	return &Indexer{
		cfg:           cfg,
		backend:       backend,
		log:           logging.Default(),
		effectivePoll: cfg.PollInterval,
	}
}

// Start performs an initial refresh, starts polling, and optionally NOTIFY listeners.
// ctx cancels the poll loop when cancelled; Stop() also cancels.
func (i *Indexer) Start(ctx context.Context) error {
	i.mu.Lock()
	if i.running {
		i.mu.Unlock()
		return nil
	}
	if i.backend == nil {
		i.mu.Unlock()
		return fmt.Errorf("catalog indexer: transfer backend is nil")
	}
	if i.cfg.ZoneName == "" {
		i.mu.Unlock()
		return fmt.Errorf("catalog indexer: zone_name is required")
	}

	runCtx, cancel := context.WithCancel(ctx)
	i.cancel = cancel
	i.running = true
	i.mu.Unlock()

	logging.LogInternalEvent(i.log, "catalog_indexer_starting", slog.LevelInfo,
		slog.String("zone", i.cfg.ZoneName),
		slog.Float64("poll_interval", i.cfg.PollInterval.Seconds()),
	)

	if err := i.Refresh(runCtx); err != nil {
		logging.LogInternalEvent(i.log, "catalog_initial_axfr_failed", slog.LevelWarn,
			slog.String("zone", i.cfg.ZoneName),
			slog.String("error", err.Error()),
		)
	}

	if err := i.startNotifyListeners(); err != nil {
		logging.LogInternalEvent(i.log, "catalog_notify_start_failed", slog.LevelWarn,
			slog.String("error", err.Error()),
		)
	}

	i.wg.Add(1)
	go i.pollLoop(runCtx)

	logging.LogInternalEvent(i.log, "catalog_indexer_started", slog.LevelInfo,
		slog.String("zone", i.cfg.ZoneName),
	)
	return nil
}

// Stop cancels background work and shuts down NOTIFY listeners.
func (i *Indexer) Stop() {
	i.mu.Lock()
	if !i.running {
		i.mu.Unlock()
		return
	}
	i.running = false
	cancel := i.cancel
	listener := i.notifyListener
	i.cancel = nil
	i.notifyListener = nil
	i.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if listener != nil {
		_ = listener.Stop()
	}
	i.wg.Wait()
	logging.LogInternalEvent(i.log, "catalog_indexer_stopped", slog.LevelInfo,
		slog.String("zone", i.cfg.ZoneName),
	)
}

// Connected reports whether the catalog zone has been loaded at least once.
func (i *Indexer) Connected() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.zone != nil
}

// ZoneName returns the configured catalog zone name.
func (i *Indexer) ZoneName() string {
	return i.cfg.ZoneName
}

// PeekZone returns the last AXFR'd catalog zone, or nil if not loaded.
func (i *Indexer) PeekZone() *dnsx.Zone {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.zone
}

// Serial returns the last known SOA serial.
func (i *Indexer) Serial() (uint32, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.serial == nil {
		return 0, false
	}
	return *i.serial, true
}

// PollInterval returns the effective poll interval in seconds.
func (i *Indexer) PollInterval() float64 {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.effectivePoll > 0 {
		return i.effectivePoll.Seconds()
	}
	return i.cfg.PollInterval.Seconds()
}

// ListZones returns discovered member zone names.
// Returns an error if the catalog zone has not been loaded yet.
func (i *Indexer) ListZones() ([]string, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.zone == nil {
		return nil, fmt.Errorf("catalog zone not yet loaded")
	}
	out := make([]string, len(i.members))
	copy(out, i.members)
	return out, nil
}

// Status returns a snapshot suitable for API responses.
func (i *Indexer) Status() Status {
	i.mu.RLock()
	defer i.mu.RUnlock()

	st := Status{
		Enabled:      i.cfg.Enabled,
		ZoneName:     i.cfg.ZoneName,
		Connected:    i.zone != nil,
		Running:      i.running,
		PollInterval: i.effectivePoll.Seconds(),
	}
	if i.serial != nil {
		s := *i.serial
		st.Serial = &s
	}
	st.ZonesDiscovered = len(i.members)
	return st
}

// CurrentSerial returns the last known SOA serial, or nil if unknown.
func (i *Indexer) CurrentSerial() *uint32 {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.serial == nil {
		return nil
	}
	s := *i.serial
	return &s
}

// Refresh AXFRs the catalog zone and updates the member list.
func (i *Indexer) Refresh(ctx context.Context) error {
	i.refreshMu.Lock()
	defer i.refreshMu.Unlock()

	if i.backend == nil {
		return fmt.Errorf("catalog indexer: transfer backend is nil")
	}

	logging.LogInternalEvent(i.log, "catalog_axfr_start", slog.LevelInfo,
		slog.String("zone", i.cfg.ZoneName),
	)

	z, err := i.backend.PerformAXFR(ctx, i.cfg.ZoneName)
	if err != nil {
		logging.LogInternalEvent(i.log, "catalog_axfr_failed", slog.LevelError,
			slog.String("zone", i.cfg.ZoneName),
			slog.String("error", err.Error()),
		)
		return err
	}

	members := ExtractMemberZones(z)
	if ver, ok := CatalogVersionTXT(z); ok && ver != CatalogVersion {
		logging.LogInternalEvent(i.log, "catalog_version_unexpected", slog.LevelWarn,
			slog.String("zone", i.cfg.ZoneName),
			slog.String("version", ver),
			slog.String("expected", CatalogVersion),
		)
	} else if !ok {
		logging.LogInternalEvent(i.log, "catalog_version_missing", slog.LevelWarn,
			slog.String("zone", i.cfg.ZoneName),
		)
	}

	var serialPtr *uint32
	if serial, ok := z.SOASerial(); ok {
		s := serial
		serialPtr = &s
	}

	poll := i.cfg.PollInterval
	if refresh, ok := z.SOARefresh(); ok && refresh > 0 {
		soaRefresh := time.Duration(refresh) * time.Second
		if soaRefresh < poll {
			poll = soaRefresh
		}
	}

	i.mu.Lock()
	i.zone = z
	i.serial = serialPtr
	i.members = members
	i.effectivePoll = poll
	i.mu.Unlock()

	serialLog := uint64(0)
	if serialPtr != nil {
		serialLog = uint64(*serialPtr)
	}
	logging.LogInternalEvent(i.log, "catalog_axfr_complete", slog.LevelInfo,
		slog.String("zone", i.cfg.ZoneName),
		slog.Uint64("serial", serialLog),
		slog.Int("zones_discovered", len(members)),
		slog.Float64("poll_interval", poll.Seconds()),
	)
	return nil
}

func (i *Indexer) pollLoop(ctx context.Context) {
	defer i.wg.Done()

	for {
		i.mu.RLock()
		interval := i.effectivePoll
		i.mu.RUnlock()
		if interval <= 0 {
			interval = i.cfg.PollInterval
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		serial, err := i.backend.QuerySOA(ctx, i.cfg.ZoneName)
		if err != nil {
			logging.LogInternalEvent(i.log, "catalog_soa_poll_failed", slog.LevelWarn,
				slog.String("zone", i.cfg.ZoneName),
				slog.String("error", err.Error()),
			)
			continue
		}

		i.mu.RLock()
		cur := i.serial
		i.mu.RUnlock()

		needRefresh := cur == nil || serial != *cur
		if !needRefresh {
			continue
		}
		logging.LogInternalEvent(i.log, "catalog_soa_changed", slog.LevelInfo,
			slog.String("zone", i.cfg.ZoneName),
			slog.Uint64("new_serial", uint64(serial)),
		)
		if err := i.Refresh(ctx); err != nil {
			logging.LogInternalEvent(i.log, "catalog_refresh_failed", slog.LevelWarn,
				slog.String("zone", i.cfg.ZoneName),
				slog.String("error", err.Error()),
			)
		}
	}
}

func (i *Indexer) startNotifyListeners() error {
	if i.cfg.NotifyUDPPort <= 0 && i.cfg.NotifyTCPPort <= 0 {
		return nil
	}
	settings := config.NotifySettings{
		Enabled:     true,
		BindAddress: i.cfg.NotifyBindAddress,
		UDPPort:     i.cfg.NotifyUDPPort,
		TCPPort:     i.cfg.NotifyTCPPort,
		RequireTSIG: false,
	}
	catalogZone := i.cfg.ZoneName
	listener := dnsx.NewNotifyListener(settings, nil, func(zone string) {
		if dnsx.NormalizeZoneName(zone) != catalogZone {
			return
		}
		logging.LogInternalEvent(i.log, "catalog_notify_received", slog.LevelInfo,
			slog.String("zone", zone),
		)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := i.Refresh(ctx); err != nil {
			logging.LogInternalEvent(i.log, "catalog_notify_refresh_failed", slog.LevelWarn,
				slog.String("zone", zone),
				slog.String("error", err.Error()),
			)
		}
	})
	if err := listener.Start(); err != nil {
		return err
	}
	i.mu.Lock()
	i.notifyListener = listener
	i.mu.Unlock()
	return nil
}
