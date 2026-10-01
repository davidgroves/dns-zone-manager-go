package httpapi

import (
	"context"

	"github.com/davidgroves/dns-zone-manager-go/internal/auth"
	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
	"github.com/davidgroves/dns-zone-manager-go/internal/live"
	"github.com/davidgroves/dns-zone-manager-go/internal/notifications"
	"github.com/davidgroves/dns-zone-manager-go/internal/store"
)

// CatalogIndexer is an optional catalog-zone discovery backend.
type CatalogIndexer interface {
	Connected() bool
	ZoneName() string
	Serial() (uint32, bool)
	ListZones() ([]string, error)
	PollInterval() float64
}

// NotifyStatusProvider reports NOTIFY listener state for health/ready.
type NotifyStatusProvider interface {
	Listening() bool
	UDPPort() int
	TCPPort() int
}

// Deps holds shared dependencies for the HTTP API.
type Deps struct {
	Settings *config.Settings
	Auth     *auth.Combined
	Client   *dnsx.Client
	Cache    *dnsx.ZoneCache
	Store    *store.Store
	Hub      *live.Hub
	Catalog  CatalogIndexer
	Notify   NotifyStatusProvider
	Emitter  *notifications.EventEmitter

	// Optional hooks used by tests / readiness.
	DNSReady   func(ctx context.Context) bool
	StoreReady func(ctx context.Context) bool
}

func (d *Deps) dnsOK(ctx context.Context) bool {
	if d.DNSReady != nil {
		return d.DNSReady(ctx)
	}
	if d.Client == nil {
		return false
	}
	return d.Client.CheckServerResponding(ctx)
}

func (d *Deps) storeOK(ctx context.Context) bool {
	if d.StoreReady != nil {
		return d.StoreReady(ctx)
	}
	if d.Store == nil {
		return true // store optional when scheduler disabled
	}
	return d.Store.Ping(ctx)
}
