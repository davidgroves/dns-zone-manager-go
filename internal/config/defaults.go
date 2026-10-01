package config

import "time"

const gib25 = 26_843_545_600 // 25 GiB

func applyDefaults(s *Settings) {
	if s.AppName == "" {
		s.AppName = "DNS Zone Manager"
	}
	if s.Server.MaxBodyBytes == 0 {
		s.Server.MaxBodyBytes = 10 << 20
	}
	if s.Server.ReadHeaderTimeout == 0 {
		s.Server.ReadHeaderTimeout = 5 * time.Second
	}
	if s.Server.ReadTimeout == 0 {
		s.Server.ReadTimeout = 30 * time.Second
	}
	if s.Server.IdleTimeout == 0 {
		s.Server.IdleTimeout = 60 * time.Second
	}
	if s.Server.ShutdownTimeout == 0 {
		s.Server.ShutdownTimeout = 15 * time.Second
	}
	if s.Live.MaxConnections == 0 {
		s.Live.MaxConnections = 500
	}
	if s.Live.MaxConnectionsPerIP == 0 {
		s.Live.MaxConnectionsPerIP = 50
	}
	if s.Live.SendTimeoutSeconds == 0 {
		s.Live.SendTimeoutSeconds = 5.0
	}
	if s.Live.PingInterval == 0 {
		s.Live.PingInterval = 30 * time.Second
	}
	if s.NSUpdate.MaxBodyBytes == 0 {
		s.NSUpdate.MaxBodyBytes = 10 << 20
	}
	if s.NSUpdate.MaxLines == 0 {
		s.NSUpdate.MaxLines = 25000
	}
	if s.NSUpdate.MaxTransactions == 0 {
		s.NSUpdate.MaxTransactions = 10000
	}
	for i := range s.TSIGKeys {
		if s.TSIGKeys[i].Algorithm == "" {
			s.TSIGKeys[i].Algorithm = "hmac-sha256"
		}
	}
	if s.DNS.Port == 0 {
		s.DNS.Port = 53
	}
	if s.DNS.Timeout == 0 {
		s.DNS.Timeout = 10 * time.Second
	}
	if s.DNS.AXFRTimeout == 0 {
		s.DNS.AXFRTimeout = 60 * time.Second
	}
	if s.DNS.UpdateTSIGKey == "" {
		s.DNS.UpdateTSIGKey = "default"
	}
	if s.DNS.PoolSize == 0 {
		s.DNS.PoolSize = 8
	}
	if s.DNS.PoolIdleTimeout == 0 {
		s.DNS.PoolIdleTimeout = 60 * time.Second
	}
	if s.APIKey.HeaderName == "" {
		s.APIKey.HeaderName = "X-API-Key"
	}
	if s.ProxyAuth.UserHeader == "" {
		s.ProxyAuth.UserHeader = "X-Auth-Request-Email"
	}
	if s.ProxyAuth.NameHeader == "" {
		s.ProxyAuth.NameHeader = "X-Auth-Request-Preferred-Username"
	}
	if s.Cache.MaxSizeBytes == 0 {
		s.Cache.MaxSizeBytes = gib25
	}
	if s.Cache.MaxZoneSizeBytes == 0 {
		s.Cache.MaxZoneSizeBytes = gib25
	}
	if s.Cache.MinRefreshInterval == 0 {
		s.Cache.MinRefreshInterval = 60
	}
	if s.Cache.MaxRefreshInterval == 0 {
		s.Cache.MaxRefreshInterval = 86400
	}
	if s.Cache.SerialRefreshDebounce == 0 {
		s.Cache.SerialRefreshDebounce = 100 * time.Millisecond
	}
	if s.Catalog.PollInterval == 0 {
		s.Catalog.PollInterval = 300
	}
	if s.Catalog.NotifyBindAddress == "" {
		s.Catalog.NotifyBindAddress = "0.0.0.0"
	}
	if s.Catalog.NotifyUDPPort == 0 {
		s.Catalog.NotifyUDPPort = 5354
	}
	if s.Catalog.NotifyTCPPort == 0 {
		s.Catalog.NotifyTCPPort = 5354
	}
	if s.Notify.BindAddress == "" {
		s.Notify.BindAddress = "0.0.0.0"
	}
	if s.Notify.UDPPort == 0 {
		s.Notify.UDPPort = 5354
	}
	if s.Notify.TCPPort == 0 {
		s.Notify.TCPPort = 5354
	}
	if s.Notify.RefreshCooldownSeconds == 0 {
		s.Notify.RefreshCooldownSeconds = 2.0
	}
	if s.Scheduler.DatabasePath == "" {
		s.Scheduler.DatabasePath = "scheduler.db"
	}
	if s.Scheduler.PollInterval == 0 {
		s.Scheduler.PollInterval = 10 * time.Second
	}
	if s.Scheduler.MaxAttempts == 0 {
		s.Scheduler.MaxAttempts = 3
	}
	if s.Scheduler.RetryBackoff == 0 {
		s.Scheduler.RetryBackoff = 60 * time.Second
	}
	if s.Scheduler.LeaseTTL == 0 {
		s.Scheduler.LeaseTTL = 120 * time.Second
	}
	if s.Scheduler.DefaultExpiryWindow == 0 {
		s.Scheduler.DefaultExpiryWindow = 3600 * time.Second
	}
	if s.Database.Backend == "" {
		s.Database.Backend = "sqlite"
	}
	if s.Database.Postgres != nil {
		applyPostgresDefaults(s.Database.Postgres)
	}
	if len(s.Retention.Statuses) == 0 {
		s.Retention.Statuses = []string{"applied", "failed", "cancelled", "expired", "reverted"}
	}
	if s.Retention.Interval == 0 {
		s.Retention.Interval = 3600 * time.Second
	}
	if s.Retention.MaxDatabaseMB == 0 {
		s.Retention.MaxDatabaseMB = 2048
	}
	if s.Retention.TrimPercent == 0 {
		s.Retention.TrimPercent = 10
	}
	if s.Retention.MaxTrimPasses == 0 {
		s.Retention.MaxTrimPasses = 10
	}
	if s.Retention.Vacuum == "" {
		s.Retention.Vacuum = "incremental"
	}
	if s.Webhooks.Timeout == 0 {
		s.Webhooks.Timeout = 5 * time.Second
	}
	if s.Webhooks.MaxRetries == 0 {
		s.Webhooks.MaxRetries = 3
	}
	if s.Webhooks.RetryBackoff == 0 {
		s.Webhooks.RetryBackoff = time.Second
	}
	if s.Webhooks.QueueSize == 0 {
		s.Webhooks.QueueSize = 1000
	}
	if len(s.Webhooks.Events) == 0 {
		s.Webhooks.Events = []string{"change_applied", "change_failed"}
	}
	for i := range s.Webhooks.Targets {
		if s.Webhooks.Targets[i].Format == "" {
			s.Webhooks.Targets[i].Format = "generic"
		}
		if s.Webhooks.Targets[i].Auth.Type == "" {
			s.Webhooks.Targets[i].Auth.Type = "none"
		}
		if s.Webhooks.Targets[i].Auth.Algorithm == "" {
			s.Webhooks.Targets[i].Auth.Algorithm = "sha256"
		}
		if s.Webhooks.Targets[i].Auth.TimestampHeader == "" {
			s.Webhooks.Targets[i].Auth.TimestampHeader = "X-DNS-Timestamp"
		}
	}
	if s.Theme.DefaultMode == "" {
		s.Theme.DefaultMode = "dark"
	}
	if s.Theme.Logo != nil && s.Theme.Logo.Alt == "" {
		s.Theme.Logo.Alt = "Home"
	}
	if s.Logging.Format == "" {
		s.Logging.Format = "json"
	}
	if s.Logging.Level == "" {
		s.Logging.Level = "INFO"
	}
	if s.Logging.SampleRate == 0 {
		s.Logging.SampleRate = 0.1
	}
	if s.Logging.SlowThresholdMS == 0 {
		s.Logging.SlowThresholdMS = 1000
	}
}

func applyPostgresDefaults(p *PostgresSettings) {
	if p.Port == 0 {
		p.Port = 5432
	}
	if p.SSLMode == "" {
		p.SSLMode = "prefer"
	}
	if p.Schema == "" {
		p.Schema = "public"
	}
	if p.PoolSize == 0 {
		p.PoolSize = 5
	}
	if p.ConnectTimeout == 0 {
		p.ConnectTimeout = 10 * time.Second
	}
	if p.StatementTimeoutMS == 0 {
		p.StatementTimeoutMS = 30000
	}
}
