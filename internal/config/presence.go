package config

import "github.com/knadh/koanf/v2"

func applyPresenceDefaults(k *koanf.Koanf, s *Settings) {
	if !k.Exists("api_key.enabled") {
		s.APIKey.Enabled = true
	}
	if !k.Exists("cache.enabled") {
		s.Cache.Enabled = true
	}
	if !k.Exists("notify.enabled") {
		s.Notify.Enabled = true
	}
	if !k.Exists("scheduler.enabled") {
		s.Scheduler.Enabled = true
	}
	if !k.Exists("database.auto_migrate") {
		s.Database.AutoMigrate = true
	}
	if !k.Exists("retention.enabled") {
		s.Retention.Enabled = true
	}
	if !k.Exists("webhooks.autorecord_manual_changes") {
		s.Webhooks.AutorecordManualChanges = true
	}
	if !k.Exists("catalog.auto_load_zones") {
		s.Catalog.AutoLoadZones = true
	}
}
