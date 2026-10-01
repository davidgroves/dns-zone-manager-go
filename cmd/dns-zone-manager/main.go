package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/davidgroves/dns-zone-manager-go/internal/auth"
	"github.com/davidgroves/dns-zone-manager-go/internal/catalog"
	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
	"github.com/davidgroves/dns-zone-manager-go/internal/httpapi"
	"github.com/davidgroves/dns-zone-manager-go/internal/live"
	"github.com/davidgroves/dns-zone-manager-go/internal/logging"
	"github.com/davidgroves/dns-zone-manager-go/internal/metrics"
	"github.com/davidgroves/dns-zone-manager-go/internal/notifications"
	"github.com/davidgroves/dns-zone-manager-go/internal/scheduler"
	"github.com/davidgroves/dns-zone-manager-go/internal/store"
	"github.com/davidgroves/dns-zone-manager-go/internal/version"
)

func main() {
	root := &cobra.Command{
		Use:     "dns-zone-manager",
		Short:   "DNS Zone Manager API server",
		Version: version.Current(),
	}
	root.AddCommand(serveCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func serveCmd() *cobra.Command {
	var (
		configPath string
		host       string
		port       int
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP API server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(configPath, host, port)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "Path to YAML config file")
	cmd.Flags().StringVar(&host, "host", "0.0.0.0", "Listen address")
	cmd.Flags().IntVar(&port, "port", 8000, "Listen port")
	return cmd
}

func runServe(configPath, host string, port int) error {
	settings, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	otlp := ""
	if settings.Logging.OTLPEndpoint != nil {
		otlp = *settings.Logging.OTLPEndpoint
	}
	level := settings.Logging.Level
	if settings.Debug {
		level = "DEBUG"
	}
	logging.Configure(settings.Logging.Format, level, otlp)
	log := logging.Default()

	metrics.Configure(metrics.Config{PerZoneLabels: settings.Metrics.PerZoneLabels})

	logging.LogInternalEvent(log, "application_startup", slog.LevelInfo,
		slog.String("dns_server", settings.DNS.Server),
		slog.Int("dns_port", settings.DNS.Port),
		slog.Bool("api_key_enabled", settings.APIKey.Enabled),
		slog.Bool("proxy_auth_enabled", settings.ProxyAuth.Enabled),
		slog.Bool("scheduler_enabled", settings.Scheduler.Enabled),
		slog.String("version", version.Current()),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client, err := dnsx.NewClient(settings)
	if err != nil {
		return fmt.Errorf("dns client: %w", err)
	}
	defer client.Close()

	cache := dnsx.NewCache(settings, client)
	hub := live.NewHub(live.HubConfig{
		MaxConnections:      settings.Live.MaxConnections,
		MaxConnectionsPerIP: settings.Live.MaxConnectionsPerIP,
		SendTimeout:         time.Duration(settings.Live.SendTimeoutSeconds * float64(time.Second)),
		PingInterval:        settings.Live.PingInterval,
	})

	var st *store.Store
	var outbox *notifications.OutboxWorker
	var emitter *notifications.EventEmitter

	if settings.Scheduler.Enabled {
		st = store.New(settings.Database, settings.Scheduler.DefaultExpiryWindow)
		if err := st.Open(ctx); err != nil {
			return fmt.Errorf("open store: %w", err)
		}
		defer func() { _ = st.Close() }()

		go scheduler.RunLoop(ctx, st, client, cache, settings.Scheduler, settings.Retention)
	}

	if settings.Webhooks.Enabled {
		emitter = &notifications.EventEmitter{
			Store:    st,
			Settings: settings.Webhooks,
			Server:   settings.DNS.Server,
		}
		client.Hooks = emitter
		if st != nil {
			outbox = notifications.NewOutboxWorker(st, settings.Webhooks)
			outbox.Start(ctx)
			defer outbox.Stop()
		}
	}

	var notifyListener *dnsx.NotifyListener
	if settings.Notify.Enabled {
		var tsig *config.TSIGKeyEntry
		if settings.Notify.RequireTSIG {
			tsig = settings.GetNotifyTSIGKey()
		}
		notifyListener = dnsx.NewNotifyListener(settings.Notify, tsig, func(zone string) {
			if cache.PeekZone(zone) == nil {
				return
			}
			refreshed, ops, err := cache.RefreshZoneWithOps(context.Background(), zone, false)
			if err != nil || refreshed == nil {
				return
			}
			payload := map[string]any{
				"type": "zone_change", "event": "change_applied",
				"trigger": "notify", "change_id": nil, "serial": refreshed.Serial,
			}
			if ops == nil {
				payload["type"] = "zone_reload"
				payload["operations"] = []any{}
			} else {
				payload["operations"] = ops
			}
			hub.Broadcast(zone, payload)
		})
		if err := notifyListener.Start(); err != nil {
			logging.LogInternalEvent(log, "notify_listener_start_failed", slog.LevelError,
				slog.String("error", err.Error()))
			notifyListener = nil
		} else {
			defer func() { _ = notifyListener.Stop() }()
		}
	}

	// Background SOA refresh
	if settings.Cache.Enabled {
		go runZoneRefresh(ctx, cache, settings)
	}

	var catIndexer *catalog.Indexer
	if settings.Catalog.Enabled && settings.Catalog.ZoneName != "" {
		catIndexer = catalog.New(catalog.ConfigFromSettings(settings), client)
		if err := catIndexer.Start(ctx); err != nil {
			logging.LogInternalEvent(log, "catalog_indexer_failed", slog.LevelError,
				slog.String("error", err.Error()))
			catIndexer = nil
		} else {
			defer catIndexer.Stop()
			go runCatalogSync(ctx, catIndexer, cache, settings)
		}
	}

	var catalogDep httpapi.CatalogIndexer
	if catIndexer != nil {
		catalogDep = catIndexer
	}

	deps := httpapi.Deps{
		Settings: settings,
		Auth:     auth.NewCombined(settings),
		Client:   client,
		Cache:    cache,
		Store:    st,
		Hub:      hub,
		Catalog:  catalogDep,
		Emitter:  emitter,
		Notify:   notifyStatus(notifyListener, settings),
	}
	handler := httpapi.New(deps)

	addr := fmt.Sprintf("%s:%d", host, port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: settings.Server.ReadHeaderTimeout,
		ReadTimeout:       settings.Server.ReadTimeout,
		IdleTimeout:       settings.Server.IdleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		logging.LogInternalEvent(log, "http_listen", slog.LevelInfo, slog.String("addr", addr))
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownTO := settings.Server.ShutdownTimeout
		if shutdownTO <= 0 {
			shutdownTO = 15 * time.Second
		}
		shCtx, cancel := context.WithTimeout(context.Background(), shutdownTO)
		defer cancel()
		logging.LogInternalEvent(log, "application_shutdown", slog.LevelInfo)
		_ = srv.Shutdown(shCtx)
		cache.InvalidateAll()
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func runCatalogSync(ctx context.Context, idx *catalog.Indexer, cache *dnsx.ZoneCache, settings *config.Settings) {
	// Brief delay so the initial AXFR can complete before the first sync.
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Second):
	}

	syncOnce := func() {
		zones, err := idx.ListZones()
		if err != nil || len(zones) == 0 {
			return
		}
		logging.LogInternalEvent(logging.Default(), "catalog_discovered", slog.LevelInfo,
			slog.Int("zones_count", len(zones)),
		)
		if !settings.Catalog.AutoLoadZones || cache == nil {
			return
		}
		results := cache.SyncFromCatalog(ctx, zones, settings.Catalog.RemoveStaleZones)
		added := 0
		for _, st := range results {
			if st == "added" {
				added++
			}
		}
		if added > 0 {
			logging.LogInternalEvent(logging.Default(), "catalog_zones_loaded", slog.LevelInfo,
				slog.Int("zones_added", added),
			)
		}
	}

	syncOnce()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			syncOnce()
		}
	}
}

func runZoneRefresh(ctx context.Context, cache *dnsx.ZoneCache, settings *config.Settings) {
	minI := settings.Cache.MinRefreshInterval
	maxI := settings.Cache.MaxRefreshInterval
	if minI <= 0 {
		minI = 60
	}
	if maxI <= 0 {
		maxI = 3600
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		for _, zone := range cache.GetZonesNeedingRefresh() {
			_, _ = cache.RefreshZone(ctx, zone, false)
		}
		sleep := time.Duration(minI) * time.Second
		if next := cache.GetNextRefreshTime(); next != nil {
			d := time.Until(*next)
			if d < time.Second {
				d = time.Second
			}
			if d > time.Duration(maxI)*time.Second {
				d = time.Duration(maxI) * time.Second
			}
			sleep = d
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(sleep):
		}
	}
}

type notifyAdapter struct {
	l *dnsx.NotifyListener
	s config.NotifySettings
}

func (n *notifyAdapter) Listening() bool { return n != nil && n.l != nil }
func (n *notifyAdapter) UDPPort() int    { return n.s.UDPPort }
func (n *notifyAdapter) TCPPort() int    { return n.s.TCPPort }

func notifyStatus(l *dnsx.NotifyListener, settings *config.Settings) httpapi.NotifyStatusProvider {
	if l == nil {
		return nil
	}
	return &notifyAdapter{l: l, s: settings.Notify}
}

// Ensure *catalog.Indexer satisfies httpapi.CatalogIndexer.
var _ httpapi.CatalogIndexer = (*catalog.Indexer)(nil)
