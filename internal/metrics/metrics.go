package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

const namespace = "dns_zone_manager"

// Config controls optional high-cardinality labels.
type Config struct {
	PerZoneLabels bool
}

var (
	defaultCfg    Config
	cfgAtRegister Config
	registerOnce  sync.Once
	zoneLabels    bool
)

// Configure sets metrics behavior. Call before Register; if Register already ran via init, this only updates defaultCfg for documentation — label sets are fixed at first Register.
func Configure(c Config) {
	defaultCfg = c
}

// Register installs all collectors on reg. Idempotent.
func Register(reg prometheus.Registerer) {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	registerOnce.Do(func() {
		cfgAtRegister = defaultCfg
		zoneLabels = cfgAtRegister.PerZoneLabels
		registerAll(reg)
	})
}

func init() {
	Register(prometheus.DefaultRegisterer)
}

var (
	loginsTotal  *prometheus.CounterVec
	logoutsTotal prometheus.Counter

	zoneSearchesTotal   counterZone
	globalSearchesTotal prometheus.Counter

	rrsetAdds     counterZone
	rrsetDeletes  counterZone
	rrsetReplaces counterZone

	ddnsUpdatesSuccessful prometheus.Counter
	ddnsUpdatesFailed     *prometheus.CounterVec

	zoneTransfersTotal        counterZoneMethod
	zoneTransfersFailed       counterZoneMethod
	zoneTransfersAbortedTotal *prometheus.CounterVec

	notifiesReceivedTotal counterZoneTransport
	notifiesRejectedTotal *prometheus.CounterVec

	cacheSizeBytes         prometheus.Gauge
	cacheEvictionsTotal    prometheus.Counter
	cacheZoneRejectedTotal *prometheus.CounterVec

	scheduledChangesCreatedTotal  prometheus.Counter
	scheduledChangesAppliedTotal  *prometheus.CounterVec
	scheduledChangesFailedTotal   *prometheus.CounterVec
	scheduledChangesExpiredTotal  prometheus.Counter
	scheduledChangesRevertedTotal prometheus.Counter
	scheduledChangesPending       prometheus.Gauge
	scheduledChangeLateness       prometheus.Histogram

	retentionChangesPurgedTotal          *prometheus.CounterVec
	retentionEventsPurgedTotal           *prometheus.CounterVec
	retentionDatabaseBytes               prometheus.Gauge
	retentionLastSuccessTimestampSeconds prometheus.Gauge
	retentionDurationSeconds             prometheus.Histogram
	retentionErrorsTotal                 prometheus.Counter

	storeOperationDuration *prometheus.HistogramVec
	storeErrorsTotal       *prometheus.CounterVec

	webhookDeliveriesTotal          *prometheus.CounterVec
	webhookDeliveryDuration         *prometheus.HistogramVec
	webhookQueueDroppedTotal        prometheus.Counter
	webhookQueueDepth               prometheus.Gauge
	webhookAutorecordedChangesTotal *prometheus.CounterVec

	zoneWSSubscribers          *prometheus.GaugeVec
	zoneWSBroadcastsTotal      *prometheus.CounterVec
	zoneWSSendErrorsTotal      prometheus.Counter
	zoneWSRejectedTotal        *prometheus.CounterVec
	zoneWSSlowClientDropsTotal prometheus.Counter

	uiLogoRequestsTotal *prometheus.CounterVec

	ddnsRoundtripSeconds       prometheus.Histogram
	httpRequestDurationSeconds *prometheus.HistogramVec
)

type counterZone struct {
	withZone *prometheus.CounterVec
	plain    prometheus.Counter
}

func (c counterZone) inc(zone string) {
	if zoneLabels {
		c.withZone.WithLabelValues(zone).Inc()
		return
	}
	c.plain.Inc()
}

type counterZoneMethod struct {
	withZone *prometheus.CounterVec
	plain    *prometheus.CounterVec
}

func (c counterZoneMethod) inc(method, zone string) {
	if zoneLabels {
		c.withZone.WithLabelValues(method, zone).Inc()
		return
	}
	c.plain.WithLabelValues(method).Inc()
}

type counterZoneTransport struct {
	withZone *prometheus.CounterVec
	plain    *prometheus.CounterVec
}

func (c counterZoneTransport) inc(transport, zone string) {
	if zoneLabels {
		c.withZone.WithLabelValues(transport, zone).Inc()
		return
	}
	c.plain.WithLabelValues(transport).Inc()
}

func registerAll(reg prometheus.Registerer) {
	mustRegister := func(c prometheus.Collector) {
		reg.MustRegister(c)
	}

	loginsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "logins_total",
		Help:      "Total number of UI login actions",
	}, []string{"auth_type"})
	logoutsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "logouts_total",
		Help:      "Total number of UI logout actions",
	})

	if zoneLabels {
		zoneSearchesTotal.withZone = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "zone_searches_total",
			Help:      "Total number of zone-specific searches",
		}, []string{"zone"})
		mustRegister(zoneSearchesTotal.withZone)
	} else {
		zoneSearchesTotal.plain = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "zone_searches_total",
			Help:      "Total number of zone-specific searches",
		})
		mustRegister(zoneSearchesTotal.plain)
	}

	globalSearchesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "global_searches_total",
		Help:      "Total number of global searches",
	})

	registerCounterZone(&rrsetAdds, "rrset_adds_total", "Total number of RRset add operations")
	registerCounterZone(&rrsetDeletes, "rrset_deletes_total", "Total number of RRset delete operations")
	registerCounterZone(&rrsetReplaces, "rrset_replaces_total", "Total number of RRset replace operations")

	ddnsUpdatesSuccessful = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "ddns_updates_successful_total",
		Help:      "Total number of successful DDNS updates",
	})
	ddnsUpdatesFailed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "ddns_updates_failed_total",
		Help:      "Total number of failed DDNS updates",
	}, []string{"reason"})

	registerCounterZoneMethod(&zoneTransfersTotal, "zone_transfers_total", "Total number of zone transfers")
	registerCounterZoneMethod(&zoneTransfersFailed, "zone_transfers_failed_total", "Total number of failed zone transfers")

	zoneTransfersAbortedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "zone_transfers_aborted_total",
		Help:      "Total number of zone transfers aborted mid-stream",
	}, []string{"method", "reason"})

	registerCounterZoneTransport(&notifiesReceivedTotal, "notifies_received_total", "Total number of NOTIFY messages received")
	notifiesRejectedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "notifies_rejected_total",
		Help:      "Total number of rejected NOTIFY messages",
	}, []string{"transport", "reason"})

	cacheSizeBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "cache_size_bytes",
		Help:      "Current cache size in bytes",
	})
	cacheEvictionsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "cache_evictions_total",
		Help:      "Total number of zones evicted from cache due to size limits",
	})
	cacheZoneRejectedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "cache_zone_rejected_total",
		Help:      "Total number of zones refused for cache insert",
	}, []string{"reason"})

	scheduledChangesCreatedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "scheduled_changes_created_total",
		Help:      "Total number of scheduled changes created",
	})
	scheduledChangesAppliedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "scheduled_changes_applied_total",
		Help:      "Total number of scheduled changes applied",
	}, []string{"trigger"})
	scheduledChangesFailedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "scheduled_changes_failed_total",
		Help:      "Total number of scheduled change execution failures",
	}, []string{"reason"})
	scheduledChangesExpiredTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "scheduled_changes_expired_total",
		Help:      "Total number of scheduled changes that expired without applying",
	})
	scheduledChangesRevertedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "scheduled_changes_reverted_total",
		Help:      "Total number of scheduled changes successfully reverted",
	})
	scheduledChangesPending = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "scheduled_changes_pending",
		Help:      "Number of pending scheduled changes (draft/scheduled/failed/running)",
	})
	scheduledChangeLateness = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "scheduled_change_lateness_seconds",
		Help:      "Seconds between scheduled_at and actual application",
		Buckets:   []float64{1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600},
	})

	retentionChangesPurgedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "retention_changes_purged_total",
		Help:      "Total number of scheduled changes purged by retention policy",
	}, []string{"reason", "status"})
	retentionEventsPurgedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "retention_events_purged_total",
		Help:      "Total number of audit events purged by retention policy",
	}, []string{"reason"})
	retentionDatabaseBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "retention_database_bytes",
		Help:      "On-disk size of the scheduled-change database in bytes",
	})
	retentionLastSuccessTimestampSeconds = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "retention_last_success_timestamp_seconds",
		Help:      "Unix timestamp of the last successful retention pass",
	})
	retentionDurationSeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "retention_duration_seconds",
		Help:      "Duration of retention maintenance passes",
		Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
	})
	retentionErrorsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "retention_errors_total",
		Help:      "Total number of retention pass failures",
	})

	storeOperationDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "store_operation_duration_seconds",
		Help:      "Duration of scheduled change store transactions",
		Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 5},
	}, []string{"operation", "backend"})
	storeErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "store_errors_total",
		Help:      "Total number of scheduled change store transactions that failed",
	}, []string{"operation", "backend"})

	webhookDeliveriesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "webhook_deliveries_total",
		Help:      "Total number of webhook delivery attempts by outcome",
	}, []string{"target", "outcome"})
	webhookDeliveryDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "webhook_delivery_duration_seconds",
		Help:      "Duration of webhook delivery requests",
		Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	}, []string{"target"})
	webhookQueueDroppedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "webhook_queue_dropped_total",
		Help:      "Total number of change events dropped because the webhook queue was full",
	})
	webhookQueueDepth = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "webhook_queue_depth",
		Help:      "Current number of change events waiting for webhook delivery",
	})
	webhookAutorecordedChangesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "webhook_autorecorded_changes_total",
		Help:      "Total number of manual changes auto-recorded in the scheduler store",
	}, []string{"outcome"})

	zoneWSSubscribers = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "zone_ws_subscribers",
		Help:      "Current number of live zone-change WebSocket subscribers",
	}, []string{"scope"})
	zoneWSBroadcastsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "zone_ws_broadcasts_total",
		Help:      "Total number of live zone-change broadcasts",
	}, []string{"scope"})
	zoneWSSendErrorsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "zone_ws_send_errors_total",
		Help:      "Total number of failed live zone-change WebSocket sends",
	})
	zoneWSRejectedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "zone_ws_rejected_total",
		Help:      "Total number of WebSocket subscriptions rejected",
	}, []string{"reason"})
	zoneWSSlowClientDropsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "zone_ws_slow_client_drops_total",
		Help:      "Total number of WebSocket clients dropped for slow sends",
	})

	uiLogoRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "ui_logo_requests_total",
		Help:      "Total number of GET /ui/logo requests",
	}, []string{"outcome"})

	ddnsRoundtripSeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "ddns_roundtrip_seconds",
		Help:      "DDNS update round-trip latency in seconds",
		Buckets:   prometheus.DefBuckets,
	})
	httpRequestDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "http_request_duration_seconds",
		Help:      "HTTP request duration in seconds",
		Buckets:   prometheus.DefBuckets,
	}, []string{"route"})

	collectors := []prometheus.Collector{
		loginsTotal, logoutsTotal, globalSearchesTotal,
		ddnsUpdatesSuccessful, ddnsUpdatesFailed,
		zoneTransfersAbortedTotal, notifiesRejectedTotal,
		cacheSizeBytes, cacheEvictionsTotal, cacheZoneRejectedTotal,
		scheduledChangesCreatedTotal, scheduledChangesAppliedTotal,
		scheduledChangesFailedTotal, scheduledChangesExpiredTotal,
		scheduledChangesRevertedTotal, scheduledChangesPending, scheduledChangeLateness,
		retentionChangesPurgedTotal, retentionEventsPurgedTotal,
		retentionDatabaseBytes, retentionLastSuccessTimestampSeconds,
		retentionDurationSeconds, retentionErrorsTotal,
		storeOperationDuration, storeErrorsTotal,
		webhookDeliveriesTotal, webhookDeliveryDuration,
		webhookQueueDroppedTotal, webhookQueueDepth, webhookAutorecordedChangesTotal,
		zoneWSSubscribers, zoneWSBroadcastsTotal, zoneWSSendErrorsTotal,
		zoneWSRejectedTotal, zoneWSSlowClientDropsTotal,
		uiLogoRequestsTotal,
		ddnsRoundtripSeconds, httpRequestDurationSeconds,
	}
	for _, c := range collectors {
		mustRegister(c)
	}
	registerCounterZoneCollectors(reg, &rrsetAdds, &rrsetDeletes, &rrsetReplaces)
	registerCounterZoneMethodCollectors(reg, &zoneTransfersTotal, &zoneTransfersFailed)
	registerCounterZoneTransportCollectors(reg, &notifiesReceivedTotal)
}

func registerCounterZone(c *counterZone, name, help string) {
	if zoneLabels {
		c.withZone = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: name, Help: help,
		}, []string{"zone"})
	} else {
		c.plain = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace, Name: name, Help: help,
		})
	}
}

func registerCounterZoneCollectors(reg prometheus.Registerer, counters ...*counterZone) {
	for _, c := range counters {
		if zoneLabels {
			reg.MustRegister(c.withZone)
		} else {
			reg.MustRegister(c.plain)
		}
	}
}

func registerCounterZoneMethod(c *counterZoneMethod, name, help string) {
	if zoneLabels {
		c.withZone = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: name, Help: help,
		}, []string{"method", "zone"})
	} else {
		c.plain = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: name, Help: help,
		}, []string{"method"})
	}
}

func registerCounterZoneMethodCollectors(reg prometheus.Registerer, counters ...*counterZoneMethod) {
	for _, c := range counters {
		if zoneLabels {
			reg.MustRegister(c.withZone)
		} else {
			reg.MustRegister(c.plain)
		}
	}
}

func registerCounterZoneTransport(c *counterZoneTransport, name, help string) {
	if zoneLabels {
		c.withZone = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: name, Help: help,
		}, []string{"transport", "zone"})
	} else {
		c.plain = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: name, Help: help,
		}, []string{"transport"})
	}
}

func registerCounterZoneTransportCollectors(reg prometheus.Registerer, counters ...*counterZoneTransport) {
	for _, c := range counters {
		if zoneLabels {
			reg.MustRegister(c.withZone)
		} else {
			reg.MustRegister(c.plain)
		}
	}
}

func IncLogins(authType string) { loginsTotal.WithLabelValues(authType).Inc() }
func IncLogouts()               { logoutsTotal.Inc() }

func IncZoneSearches(zone string) { zoneSearchesTotal.inc(zone) }
func IncGlobalSearches()          { globalSearchesTotal.Inc() }

func IncRRsetAdds(zone string)     { rrsetAdds.inc(zone) }
func IncRRsetDeletes(zone string)  { rrsetDeletes.inc(zone) }
func IncRRsetReplaces(zone string) { rrsetReplaces.inc(zone) }

func IncDDNSUpdatesSuccessful()            { ddnsUpdatesSuccessful.Inc() }
func IncDDNSUpdatesFailed(reason string)   { ddnsUpdatesFailed.WithLabelValues(reason).Inc() }
func ObserveDDNSRoundtrip(seconds float64) { ddnsRoundtripSeconds.Observe(seconds) }

func IncZoneTransfers(method, zone string)       { zoneTransfersTotal.inc(method, zone) }
func IncZoneTransfersFailed(method, zone string) { zoneTransfersFailed.inc(method, zone) }
func IncZoneTransfersAborted(method, reason string) {
	zoneTransfersAbortedTotal.WithLabelValues(method, reason).Inc()
}

func IncNotifiesReceived(transport, zone string) { notifiesReceivedTotal.inc(transport, zone) }
func IncNotifiesRejected(transport, reason string) {
	notifiesRejectedTotal.WithLabelValues(transport, reason).Inc()
}

func SetCacheSizeBytes(v float64)        { cacheSizeBytes.Set(v) }
func IncCacheEvictions()                 { cacheEvictionsTotal.Inc() }
func IncCacheZoneRejected(reason string) { cacheZoneRejectedTotal.WithLabelValues(reason).Inc() }

func IncScheduledChangesCreated() { scheduledChangesCreatedTotal.Inc() }
func IncScheduledChangesApplied(trigger string) {
	scheduledChangesAppliedTotal.WithLabelValues(trigger).Inc()
}
func IncScheduledChangesFailed(reason string) {
	scheduledChangesFailedTotal.WithLabelValues(reason).Inc()
}
func IncScheduledChangesExpired()                    { scheduledChangesExpiredTotal.Inc() }
func IncScheduledChangesReverted()                   { scheduledChangesRevertedTotal.Inc() }
func SetScheduledChangesPending(v float64)           { scheduledChangesPending.Set(v) }
func ObserveScheduledChangeLateness(seconds float64) { scheduledChangeLateness.Observe(seconds) }

func IncRetentionChangesPurged(reason, status string) {
	retentionChangesPurgedTotal.WithLabelValues(reason, status).Inc()
}
func IncRetentionEventsPurged(reason string) {
	retentionEventsPurgedTotal.WithLabelValues(reason).Inc()
}
func SetRetentionDatabaseBytes(v float64) { retentionDatabaseBytes.Set(v) }
func SetRetentionLastSuccessTimestamp(v float64) {
	retentionLastSuccessTimestampSeconds.Set(v)
}
func ObserveRetentionDuration(seconds float64) { retentionDurationSeconds.Observe(seconds) }
func IncRetentionErrors()                      { retentionErrorsTotal.Inc() }

func ObserveStoreOperation(operation, backend string, seconds float64) {
	storeOperationDuration.WithLabelValues(operation, backend).Observe(seconds)
}
func IncStoreErrors(operation, backend string) {
	storeErrorsTotal.WithLabelValues(operation, backend).Inc()
}

func IncWebhookDeliveries(target, outcome string) {
	webhookDeliveriesTotal.WithLabelValues(target, outcome).Inc()
}
func ObserveWebhookDelivery(target string, seconds float64) {
	webhookDeliveryDuration.WithLabelValues(target).Observe(seconds)
}
func IncWebhookQueueDropped()        { webhookQueueDroppedTotal.Inc() }
func SetWebhookQueueDepth(v float64) { webhookQueueDepth.Set(v) }
func IncWebhookAutorecordedChanges(outcome string) {
	webhookAutorecordedChangesTotal.WithLabelValues(outcome).Inc()
}

func SetZoneWSSubscribers(scope string, v float64) { zoneWSSubscribers.WithLabelValues(scope).Set(v) }
func IncZoneWSBroadcasts(scope string)             { zoneWSBroadcastsTotal.WithLabelValues(scope).Inc() }
func IncZoneWSSendErrors()                         { zoneWSSendErrorsTotal.Inc() }
func IncZoneWSRejected(reason string)              { zoneWSRejectedTotal.WithLabelValues(reason).Inc() }
func IncZoneWSSlowClientDrops()                    { zoneWSSlowClientDropsTotal.Inc() }

func IncUILogoRequests(outcome string) { uiLogoRequestsTotal.WithLabelValues(outcome).Inc() }

func ObserveHTTPRequestDuration(route string, seconds float64) {
	httpRequestDurationSeconds.WithLabelValues(route).Observe(seconds)
}
