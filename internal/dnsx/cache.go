package dnsx

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/logging"
	"github.com/davidgroves/dns-zone-manager-go/internal/metrics"
)

const (
	jitterMin = 0.9
	jitterMax = 1.0
)

// CachedZone holds a Zone plus refresh metadata.
type CachedZone struct {
	ZoneName    string
	Zone        *Zone
	Serial      uint32
	LastRefresh time.Time
	SOARefresh  uint32
	hasSOARef   bool

	mu     sync.RWMutex
	jitter float64
}

func newCachedZone(zoneName string, z *Zone, serial uint32) *CachedZone {
	cz := &CachedZone{
		ZoneName:    NormalizeZoneName(zoneName),
		Zone:        z,
		Serial:      serial,
		LastRefresh: time.Now().UTC(),
		jitter:      jitterMin + rand.Float64()*(jitterMax-jitterMin),
	}
	if ref, ok := z.SOARefresh(); ok {
		cz.SOARefresh = ref
		cz.hasSOARef = true
	}
	return cz
}

func (cz *CachedZone) regenerateJitter() {
	cz.jitter = jitterMin + rand.Float64()*(jitterMax-jitterMin)
}

// EffectiveRefreshInterval returns SOA refresh bounded by min/max with jitter.
func (cz *CachedZone) EffectiveRefreshInterval(minInterval, maxInterval int) int {
	cz.mu.RLock()
	defer cz.mu.RUnlock()
	base := maxInterval
	if cz.hasSOARef {
		base = int(cz.SOARefresh)
		if base < minInterval {
			base = minInterval
		}
		if base > maxInterval {
			base = maxInterval
		}
	}
	return int(float64(base) * cz.jitter)
}

func (cz *CachedZone) estimateSizeBytes() int64 {
	if cz.Zone == nil {
		return 500
	}
	return cz.Zone.SizeBytes()
}

// lruNode is an entry in the cache LRU doubly-linked list.
type lruNode struct {
	zone string
	prev *lruNode
	next *lruNode
}

// ZoneCache is a thread-safe multi-zone cache with LRU eviction.
type ZoneCache struct {
	settings *config.Settings
	backend  TransferBackend
	enabled  bool

	mu           sync.RWMutex
	zones        map[string]*CachedZone
	catalogZones map[string]struct{}
	lruHead      *lruNode // MRU
	lruTail      *lruNode // LRU
	lruIndex     map[string]*lruNode
	currentSize  int64

	debounceMu     sync.Mutex
	debounceTimers map[string]*time.Timer

	log *slog.Logger
}

// NewCache creates a ZoneCache backed by client.
func NewCache(settings *config.Settings, client *Client) *ZoneCache {
	return NewCacheWithBackend(settings, client)
}

// NewCacheWithBackend creates a ZoneCache with an injectable TransferBackend.
func NewCacheWithBackend(settings *config.Settings, backend TransferBackend) *ZoneCache {
	enabled := true
	if settings != nil {
		enabled = settings.Cache.Enabled
	}
	return &ZoneCache{
		settings:       settings,
		backend:        backend,
		enabled:        enabled,
		zones:          make(map[string]*CachedZone),
		catalogZones:   make(map[string]struct{}),
		lruIndex:       make(map[string]*lruNode),
		debounceTimers: make(map[string]*time.Timer),
		log:            logging.Default(),
	}
}

func (c *ZoneCache) normalize(zone string) string {
	return NormalizeZoneName(zone)
}

func (c *ZoneCache) markUsed(zone string) {
	n, ok := c.lruIndex[zone]
	if !ok {
		n = &lruNode{zone: zone}
		c.lruIndex[zone] = n
		c.pushFront(n)
		return
	}
	c.detach(n)
	c.pushFront(n)
}

func (c *ZoneCache) pushFront(n *lruNode) {
	n.prev = nil
	n.next = c.lruHead
	if c.lruHead != nil {
		c.lruHead.prev = n
	}
	c.lruHead = n
	if c.lruTail == nil {
		c.lruTail = n
	}
}

func (c *ZoneCache) detach(n *lruNode) {
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		c.lruHead = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	} else {
		c.lruTail = n.prev
	}
	n.prev, n.next = nil, nil
}

func (c *ZoneCache) removeLRU(zone string) {
	if n, ok := c.lruIndex[zone]; ok {
		c.detach(n)
		delete(c.lruIndex, zone)
	}
}

func (c *ZoneCache) evictIfNeeded(requiredBytes int64) error {
	maxSize := c.settings.Cache.MaxSizeBytes
	maxZone := c.settings.Cache.EffectiveMaxZoneSizeBytes()
	if maxZone > 0 && requiredBytes > maxZone {
		metrics.IncCacheZoneRejected("oversize")
		return &ZoneTransferError{
			Message: fmt.Sprintf("Zone exceeds max_zone_size_bytes (%d > %d)", requiredBytes, maxZone),
		}
	}
	if maxSize == 0 {
		return nil
	}
	if requiredBytes > maxSize {
		metrics.IncCacheZoneRejected("oversize")
		return &ZoneTransferError{
			Message: fmt.Sprintf("Zone exceeds cache max_size_bytes (%d > %d)", requiredBytes, maxSize),
		}
	}
	for c.currentSize+requiredBytes > maxSize && c.lruTail != nil {
		victim := c.lruTail.zone
		cached := c.zones[victim]
		evictedSize := int64(0)
		if cached != nil {
			evictedSize = cached.estimateSizeBytes()
		}
		delete(c.zones, victim)
		delete(c.catalogZones, victim)
		c.removeLRU(victim)
		c.currentSize -= evictedSize
		metrics.IncCacheEvictions()
		logging.LogInternalEvent(c.log, "cache_evicted", slog.LevelInfo,
			slog.String("zone", victim),
			slog.Int64("evicted_size_bytes", evictedSize),
			slog.Int64("current_size_bytes", c.currentSize),
			slog.String("reason", "lru"),
		)
	}
	metrics.SetCacheSizeBytes(float64(c.currentSize))
	return nil
}

// PeekZone returns a cached zone without triggering AXFR.
func (c *ZoneCache) PeekZone(zone string) *CachedZone {
	zone = c.normalize(zone)
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.zones[zone]
}

// GetZone returns a cached zone, loading via AXFR when enabled and missing.
func (c *ZoneCache) GetZone(ctx context.Context, zone string) *CachedZone {
	zone = c.normalize(zone)
	c.mu.Lock()
	cached := c.zones[zone]
	if cached != nil {
		c.markUsed(zone)
		c.mu.Unlock()
		return cached
	}
	c.mu.Unlock()

	if !c.enabled {
		return nil
	}
	cached, err := c.RefreshZone(ctx, zone, false)
	if err != nil {
		return nil
	}
	return cached
}

// LoadZone forces an AXFR load into the cache.
func (c *ZoneCache) LoadZone(ctx context.Context, zone string) (*CachedZone, error) {
	return c.RefreshZone(ctx, zone, true)
}

// RefreshZone refreshes a zone, preferring IXFR when possible.
func (c *ZoneCache) RefreshZone(ctx context.Context, zone string, forceAXFR bool) (*CachedZone, error) {
	cached, _, err := c.RefreshZoneWithOps(ctx, zone, forceAXFR)
	return cached, err
}

// RefreshZoneWithOps refreshes and returns live-update operations when IXFR succeeds.
// ops is [] when serial unchanged, nil when full AXFR/reload happened.
func (c *ZoneCache) RefreshZoneWithOps(ctx context.Context, zone string, forceAXFR bool) (*CachedZone, []Operation, error) {
	zone = c.normalize(zone)

	c.mu.RLock()
	existing := c.zones[zone]
	c.mu.RUnlock()

	preferIXFR := true
	if c.settings != nil {
		preferIXFR = c.settings.Notify.PreferIXFR
	}

	if existing != nil && preferIXFR && !forceAXFR {
		result, ops, ok := c.refreshZoneIXFR(ctx, zone, existing)
		if ok {
			return result, ops, nil
		}
		logging.LogInternalEvent(c.log, "ixfr_fallback_to_axfr", slog.LevelInfo,
			slog.String("zone", zone),
			slog.Uint64("cached_serial", uint64(existing.Serial)),
		)
	}
	cached, err := c.refreshZoneAXFR(ctx, zone)
	return cached, nil, err
}

func (c *ZoneCache) refreshZoneAXFR(ctx context.Context, zone string) (*CachedZone, error) {
	logging.LogInternalEvent(c.log, "zone_refresh_start", slog.LevelInfo,
		slog.String("zone", zone),
		slog.String("method", "axfr"),
	)
	z, err := c.backend.PerformAXFR(ctx, zone)
	if err != nil {
		metrics.IncZoneTransfersFailed("axfr", zone)
		return nil, err
	}
	serial, _ := z.SOASerial()
	cached := newCachedZone(zone, z, serial)
	newSize := cached.estimateSizeBytes()

	c.mu.Lock()
	defer c.mu.Unlock()

	old := c.zones[zone]
	oldSize := int64(0)
	if old != nil {
		oldSize = old.estimateSizeBytes()
		c.currentSize -= oldSize
	}
	if err := c.evictIfNeeded(newSize); err != nil {
		if old != nil {
			c.currentSize += oldSize
		}
		metrics.IncZoneTransfersFailed("axfr", zone)
		return nil, err
	}
	c.zones[zone] = cached
	c.markUsed(zone)
	c.currentSize += newSize
	metrics.SetCacheSizeBytes(float64(c.currentSize))

	logging.LogInternalEvent(c.log, "zone_refresh_complete", slog.LevelInfo,
		slog.String("zone", zone),
		slog.Uint64("serial", uint64(serial)),
		slog.Int("rrset_count", z.RRsetCount()),
		slog.String("method", "axfr"),
		slog.Int64("size_bytes", newSize),
	)
	return cached, nil
}

func (c *ZoneCache) refreshZoneIXFR(ctx context.Context, zone string, cached *CachedZone) (*CachedZone, []Operation, bool) {
	serverSerial, err := c.backend.QuerySOA(ctx, zone)
	if err != nil {
		logging.LogInternalEvent(c.log, "ixfr_serial_query_failed", slog.LevelWarn,
			slog.String("zone", zone),
			slog.String("error", err.Error()),
		)
		return nil, nil, false
	}

	cached.mu.Lock()
	curSerial := cached.Serial
	cached.mu.Unlock()

	if serverSerial == curSerial {
		logging.LogInternalEvent(c.log, "zone_refresh_skipped", slog.LevelInfo,
			slog.String("zone", zone),
			slog.Uint64("serial", uint64(curSerial)),
			slog.String("reason", "serial_unchanged"),
		)
		cached.mu.Lock()
		cached.LastRefresh = time.Now().UTC()
		cached.regenerateJitter()
		cached.mu.Unlock()
		return cached, []Operation{}, true
	}

	if !Less(curSerial, serverSerial) {
		return nil, nil, false
	}

	logging.LogInternalEvent(c.log, "zone_refresh_start", slog.LevelInfo,
		slog.String("zone", zone),
		slog.String("method", "ixfr"),
		slog.Uint64("from_serial", uint64(curSerial)),
		slog.Uint64("to_serial", uint64(serverSerial)),
	)

	ixfr, err := c.backend.PerformIXFR(ctx, zone, curSerial)
	if err != nil {
		logging.LogInternalEvent(c.log, "ixfr_failed", slog.LevelWarn,
			slog.String("zone", zone),
			slog.String("error", err.Error()),
		)
		metrics.IncZoneTransfersFailed("ixfr", zone)
		return nil, nil, false
	}
	if ixfr.IsFullAXFR {
		logging.LogInternalEvent(c.log, "ixfr_server_returned_axfr", slog.LevelInfo,
			slog.String("zone", zone),
			slog.Uint64("from_serial", uint64(curSerial)),
		)
		return nil, nil, false
	}

	cached.mu.Lock()
	defer cached.mu.Unlock()

	// Apply operations in order for multi-delta correctness.
	if len(ixfr.Operations) > 0 {
		for _, op := range ixfr.Operations {
			typ, err := TypeFromString(op.Type)
			if err != nil {
				continue
			}
			class, err := ClassFromString(op.Class)
			if err != nil {
				class = dns.ClassINET
			}
			switch op.Action {
			case "delete":
				_ = cached.Zone.DeleteRecords(op.Name, typ, class, op.Records)
			case "add":
				_ = cached.Zone.AddRecords(op.Name, typ, class, op.TTL, op.Records)
			}
		}
		_ = cached.Zone.SetSOASerial(ixfr.NewSerial)
	} else if err := cached.Zone.ApplyIXFRChanges(ixfr.Deletes, ixfr.Adds, ixfr.NewSerial); err != nil {
		logging.LogInternalEvent(c.log, "ixfr_apply_failed", slog.LevelError,
			slog.String("zone", zone),
			slog.String("error", err.Error()),
		)
		metrics.IncZoneTransfersFailed("ixfr", zone)
		return nil, nil, false
	}
	cached.Serial = ixfr.NewSerial
	cached.LastRefresh = time.Now().UTC()
	cached.regenerateJitter()
	if ref, ok := cached.Zone.SOARefresh(); ok {
		cached.SOARefresh = ref
		cached.hasSOARef = true
	}

	deletesN, addsN := len(ixfr.Deletes), len(ixfr.Adds)
	if deletesN == 0 && addsN == 0 {
		for _, op := range ixfr.Operations {
			switch op.Action {
			case "delete":
				deletesN++
			case "add":
				addsN++
			}
		}
	}

	logging.LogInternalEvent(c.log, "zone_refresh_complete", slog.LevelInfo,
		slog.String("zone", zone),
		slog.Uint64("serial", uint64(cached.Serial)),
		slog.Int("rrset_count", cached.Zone.RRsetCount()),
		slog.String("method", "ixfr"),
		slog.Int("deletes", deletesN),
		slog.Int("adds", addsN),
	)
	return cached, ixfr.Operations, true
}

// Invalidate removes one zone from the cache.
func (c *ZoneCache) Invalidate(zone string) {
	zone = c.normalize(zone)
	c.mu.Lock()
	defer c.mu.Unlock()
	if cached, ok := c.zones[zone]; ok {
		c.currentSize -= cached.estimateSizeBytes()
		delete(c.zones, zone)
		delete(c.catalogZones, zone)
		c.removeLRU(zone)
		metrics.SetCacheSizeBytes(float64(c.currentSize))
	}
}

// InvalidateAll clears the cache.
func (c *ZoneCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.zones = make(map[string]*CachedZone)
	c.catalogZones = make(map[string]struct{})
	c.lruHead, c.lruTail = nil, nil
	c.lruIndex = make(map[string]*lruNode)
	c.currentSize = 0
	metrics.SetCacheSizeBytes(0)
}

// ListZones returns cached zone names.
func (c *ZoneCache) ListZones() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.zones))
	for z := range c.zones {
		out = append(out, z)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

// ListZonesPaginated returns paginated cached zones.
func (c *ZoneCache) ListZonesPaginated(after string, limit, offset *int) (zones []*CachedZone, total int, next *string, hasMore bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	names := make([]string, 0, len(c.zones))
	for z := range c.zones {
		names = append(names, z)
	}
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	total = len(names)

	if after == "" && limit == nil && offset == nil {
		for _, n := range names {
			zones = append(zones, c.zones[n])
		}
		return zones, total, nil, false
	}

	start := 0
	if offset != nil {
		start = *offset
		if start < 0 {
			start = 0
		}
		if start > total {
			start = total
		}
	} else if after != "" {
		afterLower := strings.ToLower(after)
		start = total
		for i, n := range names {
			if strings.ToLower(n) >= afterLower {
				start = i
				break
			}
		}
	}

	end := total
	if limit != nil && start+*limit < end {
		end = start + *limit
		hasMore = end < total
	}
	for _, n := range names[start:end] {
		zones = append(zones, c.zones[n])
	}
	if hasMore && end < total {
		nxt := names[end]
		next = &nxt
	}
	return zones, total, next, hasMore
}

// GetAllRRsets returns paginated RRsets for a zone.
func (c *ZoneCache) GetAllRRsets(zone string, offset, limit int, after *RRsetCursor) (items []RRsetInfo, total int, next *RRsetCursor, hasMore bool, ok bool) {
	cached := c.GetZone(context.Background(), zone)
	if cached == nil {
		return nil, 0, nil, false, false
	}
	items, total, next, hasMore = cached.Zone.ListRRsets(offset, limit, after)
	return items, total, next, hasMore, true
}

// SearchRRsets searches within one zone.
func (c *ZoneCache) SearchRRsets(zone string, namePattern, valuePattern *regexp.Regexp, typ *uint16, offset, limit int, after *NameCursor) (items []RRsetInfo, total int, next *NameCursor, hasMore bool, ok bool) {
	cached := c.GetZone(context.Background(), zone)
	if cached == nil {
		return nil, 0, nil, false, false
	}
	items, total, next, hasMore = cached.Zone.Search(namePattern, valuePattern, typ, offset, limit, after)
	return items, total, next, hasMore, true
}

// ZoneSearchResult groups search hits for one zone.
type ZoneSearchResult struct {
	Zone   string
	Serial uint32
	RRsets []RRsetInfo
}

// SearchAllZones searches across all cached zones.
func (c *ZoneCache) SearchAllZones(namePattern, valuePattern *regexp.Regexp, typ *uint16, offset, limit int, afterZone, afterName string) (results []ZoneSearchResult, total int, nextZone, nextName string, hasMore bool) {
	c.mu.RLock()
	zones := make([]*CachedZone, 0, len(c.zones))
	for _, z := range c.zones {
		zones = append(zones, z)
	}
	c.mu.RUnlock()

	type hit struct {
		zone   string
		serial uint32
		info   RRsetInfo
	}
	var all []hit
	for _, cz := range zones {
		items, _, _, _ := cz.Zone.Search(namePattern, valuePattern, typ, 0, 0, nil)
		for _, it := range items {
			all = append(all, hit{zone: cz.ZoneName, serial: cz.Serial, info: it})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		zi, zj := strings.ToLower(all[i].zone), strings.ToLower(all[j].zone)
		if zi != zj {
			return zi < zj
		}
		ni, nj := strings.ToLower(all[i].info.Name), strings.ToLower(all[j].info.Name)
		if ni != nj {
			return ni < nj
		}
		return all[i].info.Type < all[j].info.Type
	})
	total = len(all)

	start := 0
	if offset > 0 {
		start = offset
		if start > total {
			start = total
		}
	} else if afterZone != "" || afterName != "" {
		az, an := strings.ToLower(afterZone), strings.ToLower(afterName)
		start = total
		for i, h := range all {
			if strings.ToLower(h.zone) > az || (strings.ToLower(h.zone) == az && strings.ToLower(h.info.Name) >= an) {
				start = i
				break
			}
		}
	}

	end := total
	if limit > 0 && start+limit < end {
		end = start + limit
		hasMore = end < total
	}

	grouped := make(map[string]*ZoneSearchResult)
	var order []string
	for _, h := range all[start:end] {
		gs, ok := grouped[h.zone]
		if !ok {
			gs = &ZoneSearchResult{Zone: h.zone, Serial: h.serial}
			grouped[h.zone] = gs
			order = append(order, h.zone)
		}
		gs.RRsets = append(gs.RRsets, h.info)
	}
	for _, z := range order {
		results = append(results, *grouped[z])
	}
	if hasMore && end < total {
		nextZone = all[end].zone
		nextName = all[end].info.Name
	}
	return results, total, nextZone, nextName, hasMore
}

// UpdateCacheAfterAdd applies an optimistic add then schedules a debounced SOA refresh.
func (c *ZoneCache) UpdateCacheAfterAdd(zone, name string, ttl uint32, rdtype, rdclass string, records []string) {
	cz := c.PeekZone(zone)
	if cz == nil {
		return
	}
	typ, err := TypeFromString(rdtype)
	if err != nil {
		return
	}
	class, err := ClassFromString(rdclass)
	if err != nil {
		class = dns.ClassINET
	}
	_ = cz.Zone.AddRecords(name, typ, class, ttl, records)
	c.scheduleSerialRefresh(c.normalize(zone))
}

// UpdateCacheAfterDelete applies an optimistic delete then schedules a debounced SOA refresh.
func (c *ZoneCache) UpdateCacheAfterDelete(zone, name, rdtype, rdclass string, records []string) {
	cz := c.PeekZone(zone)
	if cz == nil {
		return
	}
	typ, err := TypeFromString(rdtype)
	if err != nil {
		return
	}
	class, err := ClassFromString(rdclass)
	if err != nil {
		class = dns.ClassINET
	}
	_ = cz.Zone.DeleteRecords(name, typ, class, records)
	c.scheduleSerialRefresh(c.normalize(zone))
}

// UpdateCacheAfterReplace applies an optimistic replace then schedules a debounced SOA refresh.
func (c *ZoneCache) UpdateCacheAfterReplace(zone, name string, ttl uint32, rdtype, rdclass string, records []string) {
	cz := c.PeekZone(zone)
	if cz == nil {
		return
	}
	typ, err := TypeFromString(rdtype)
	if err != nil {
		return
	}
	class, err := ClassFromString(rdclass)
	if err != nil {
		class = dns.ClassINET
	}
	_ = cz.Zone.ReplaceRRset(name, typ, class, ttl, records)
	c.scheduleSerialRefresh(c.normalize(zone))
}

func (c *ZoneCache) scheduleSerialRefresh(zone string) {
	debounce := 100 * time.Millisecond
	if c.settings != nil && c.settings.Cache.SerialRefreshDebounce > 0 {
		debounce = c.settings.Cache.SerialRefreshDebounce
	}
	c.debounceMu.Lock()
	defer c.debounceMu.Unlock()
	if t, ok := c.debounceTimers[zone]; ok {
		t.Reset(debounce)
		return
	}
	c.debounceTimers[zone] = time.AfterFunc(debounce, func() {
		c.refreshZoneSerial(context.Background(), zone)
		c.debounceMu.Lock()
		delete(c.debounceTimers, zone)
		c.debounceMu.Unlock()
	})
}

func (c *ZoneCache) refreshZoneSerial(ctx context.Context, zone string) *uint32 {
	cz := c.PeekZone(zone)
	if cz == nil {
		return nil
	}
	serial, err := c.backend.QuerySOA(ctx, zone)
	if err != nil {
		return nil
	}
	cz.mu.Lock()
	cz.Serial = serial
	_ = cz.Zone.SetSOASerial(serial)
	cz.mu.Unlock()
	return &serial
}

// SyncFromCatalog loads missing catalog zones and optionally removes stale ones.
func (c *ZoneCache) SyncFromCatalog(ctx context.Context, zones []string, removeStale bool) map[string]string {
	results := make(map[string]string)
	normalized := make(map[string]struct{}, len(zones))
	for _, z := range zones {
		normalized[c.normalize(z)] = struct{}{}
	}

	if removeStale {
		c.mu.Lock()
		for z := range c.catalogZones {
			if _, ok := normalized[z]; ok {
				continue
			}
			if cached, exists := c.zones[z]; exists {
				c.currentSize -= cached.estimateSizeBytes()
				delete(c.zones, z)
				c.removeLRU(z)
			}
			delete(c.catalogZones, z)
			results[z] = "removed"
			logging.LogInternalEvent(c.log, "catalog_zone_removed", slog.LevelInfo,
				slog.String("zone", z),
			)
		}
		metrics.SetCacheSizeBytes(float64(c.currentSize))
		c.mu.Unlock()
	}

	for _, zoneName := range zones {
		zone := c.normalize(zoneName)
		c.mu.Lock()
		_, exists := c.zones[zone]
		if exists {
			c.catalogZones[zone] = struct{}{}
			c.markUsed(zone)
			c.mu.Unlock()
			results[zone] = "exists"
			continue
		}
		c.mu.Unlock()

		logging.LogInternalEvent(c.log, "catalog_zone_loading", slog.LevelInfo,
			slog.String("zone", zone),
		)
		z, err := c.backend.PerformAXFR(ctx, zone)
		if err != nil {
			logging.LogInternalEvent(c.log, "catalog_zone_load_failed", slog.LevelWarn,
				slog.String("zone", zone),
				slog.String("error", err.Error()),
			)
			results[zone] = "failed"
			continue
		}
		serial, _ := z.SOASerial()
		cached := newCachedZone(zone, z, serial)
		newSize := cached.estimateSizeBytes()

		c.mu.Lock()
		if err := c.evictIfNeeded(newSize); err != nil {
			c.mu.Unlock()
			results[zone] = "failed"
			continue
		}
		c.zones[zone] = cached
		c.catalogZones[zone] = struct{}{}
		c.markUsed(zone)
		c.currentSize += newSize
		metrics.SetCacheSizeBytes(float64(c.currentSize))
		c.mu.Unlock()

		results[zone] = "added"
		logging.LogInternalEvent(c.log, "catalog_zone_added", slog.LevelInfo,
			slog.String("zone", zone),
			slog.Uint64("serial", uint64(serial)),
			slog.Int("rrset_count", z.RRsetCount()),
			slog.Int64("size_bytes", newSize),
		)
	}
	return results
}

// GetZonesNeedingRefresh returns zones past their effective refresh interval.
func (c *ZoneCache) GetZonesNeedingRefresh() []string {
	now := time.Now().UTC()
	minI := c.settings.Cache.MinRefreshInterval
	maxI := c.settings.Cache.MaxRefreshInterval
	var out []string
	c.mu.RLock()
	defer c.mu.RUnlock()
	for name, cz := range c.zones {
		interval := cz.EffectiveRefreshInterval(minI, maxI)
		if now.After(cz.LastRefresh.Add(time.Duration(interval) * time.Second)) {
			out = append(out, name)
		}
	}
	return out
}

// GetNextRefreshTime returns the earliest next refresh across all zones.
func (c *ZoneCache) GetNextRefreshTime() *time.Time {
	minI := c.settings.Cache.MinRefreshInterval
	maxI := c.settings.Cache.MaxRefreshInterval
	var earliest *time.Time
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, cz := range c.zones {
		interval := cz.EffectiveRefreshInterval(minI, maxI)
		next := cz.LastRefresh.Add(time.Duration(interval) * time.Second)
		if earliest == nil || next.Before(*earliest) {
			t := next
			earliest = &t
		}
	}
	return earliest
}

// SizeBytes returns the current estimated cache size.
func (c *ZoneCache) SizeBytes() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentSize
}

// Len returns the number of cached zones.
func (c *ZoneCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.zones)
}
