package config

import "time"

// Settings is the root application configuration.
type Settings struct {
	AppName   string            `mapstructure:"app_name" yaml:"app_name"`
	Debug     bool              `mapstructure:"debug" yaml:"debug"`
	Server    ServerSettings    `mapstructure:"server" yaml:"server"`
	Live      LiveSettings      `mapstructure:"live" yaml:"live"`
	NSUpdate  NSUpdateSettings  `mapstructure:"nsupdate" yaml:"nsupdate"`
	TSIGKeys  []TSIGKeyEntry    `mapstructure:"tsig_keys" yaml:"tsig_keys"`
	DNS       DNSSettings       `mapstructure:"dns" yaml:"dns"`
	APIKey    APIKeySettings    `mapstructure:"api_key" yaml:"api_key"`
	ProxyAuth ProxyAuthSettings `mapstructure:"proxy_auth" yaml:"proxy_auth"`
	Cache     CacheSettings     `mapstructure:"cache" yaml:"cache"`
	Catalog   CatalogSettings   `mapstructure:"catalog" yaml:"catalog"`
	RNDC      RNDCSettings      `mapstructure:"rndc" yaml:"rndc"`
	Notify    NotifySettings    `mapstructure:"notify" yaml:"notify"`
	Scheduler SchedulerSettings `mapstructure:"scheduler" yaml:"scheduler"`
	Database  DatabaseSettings  `mapstructure:"database" yaml:"database"`
	Retention RetentionSettings `mapstructure:"retention" yaml:"retention"`
	Webhooks  WebhookSettings   `mapstructure:"webhooks" yaml:"webhooks"`
	Theme     ThemeSettings     `mapstructure:"theme" yaml:"theme"`
	Logging   LoggingSettings   `mapstructure:"logging" yaml:"logging"`
	Metrics   MetricsSettings   `mapstructure:"metrics" yaml:"metrics"`
}

type TSIGKeyEntry struct {
	Name       string `mapstructure:"name" yaml:"name"`
	Secret     Secret `mapstructure:"secret" yaml:"secret"`
	SecretFile string `mapstructure:"secret_file" yaml:"secret_file"`
	Algorithm  string `mapstructure:"algorithm" yaml:"algorithm"`
}

type ServerSettings struct {
	CorsOrigins       []string      `mapstructure:"cors_origins" yaml:"cors_origins"`
	MaxBodyBytes      int64         `mapstructure:"max_body_bytes" yaml:"max_body_bytes"`
	ReadHeaderTimeout time.Duration `mapstructure:"read_header_timeout" yaml:"read_header_timeout"`
	ReadTimeout       time.Duration `mapstructure:"read_timeout" yaml:"read_timeout"`
	IdleTimeout       time.Duration `mapstructure:"idle_timeout" yaml:"idle_timeout"`
	ShutdownTimeout   time.Duration `mapstructure:"shutdown_timeout" yaml:"shutdown_timeout"`
}

type LiveSettings struct {
	MaxConnections      int           `mapstructure:"max_connections" yaml:"max_connections"`
	MaxConnectionsPerIP int           `mapstructure:"max_connections_per_ip" yaml:"max_connections_per_ip"`
	SendTimeoutSeconds  float64       `mapstructure:"send_timeout_seconds" yaml:"send_timeout_seconds"`
	PingInterval        time.Duration `mapstructure:"ping_interval" yaml:"ping_interval"`
}

type NSUpdateSettings struct {
	MaxBodyBytes    int64 `mapstructure:"max_body_bytes" yaml:"max_body_bytes"`
	MaxLines        int   `mapstructure:"max_lines" yaml:"max_lines"`
	MaxTransactions int   `mapstructure:"max_transactions" yaml:"max_transactions"`
}

type DNSSettings struct {
	Server          string        `mapstructure:"server" yaml:"server"`
	Port            int           `mapstructure:"port" yaml:"port"`
	TCPPort         *int          `mapstructure:"tcp_port" yaml:"tcp_port"`
	Timeout         time.Duration `mapstructure:"timeout" yaml:"timeout"`
	AXFRTimeout     time.Duration `mapstructure:"axfr_timeout" yaml:"axfr_timeout"`
	UpdateTSIGKey   string        `mapstructure:"update_tsig_key" yaml:"update_tsig_key"`
	AXFRTSIGKey     *string       `mapstructure:"axfr_tsig_key" yaml:"axfr_tsig_key"`
	PoolSize        int           `mapstructure:"pool_size" yaml:"pool_size"`
	PoolIdleTimeout time.Duration `mapstructure:"pool_idle_timeout" yaml:"pool_idle_timeout"`
}

type APIKeyEntry struct {
	Name       string `mapstructure:"name" yaml:"name"`
	Secret     Secret `mapstructure:"secret" yaml:"secret"`
	SecretFile string `mapstructure:"secret_file" yaml:"secret_file"`
}

type APIKeySettings struct {
	Enabled    bool              `mapstructure:"enabled" yaml:"enabled"`
	Keys       map[string]Secret `mapstructure:"-" yaml:"-"`
	KeysList   []APIKeyEntry     `mapstructure:"keys" yaml:"keys"`
	KeysStr    string            `mapstructure:"keys_str" yaml:"-"`
	HeaderName string            `mapstructure:"header_name" yaml:"header_name"`
}

type ProxyAuthSettings struct {
	Enabled    bool   `mapstructure:"enabled" yaml:"enabled"`
	UserHeader string `mapstructure:"user_header" yaml:"user_header"`
	NameHeader string `mapstructure:"name_header" yaml:"name_header"`
}

type CacheSettings struct {
	Enabled               bool          `mapstructure:"enabled" yaml:"enabled"`
	MinRefreshInterval    int           `mapstructure:"min_refresh_interval" yaml:"min_refresh_interval"`
	MaxRefreshInterval    int           `mapstructure:"max_refresh_interval" yaml:"max_refresh_interval"`
	MaxSizeBytes          int64         `mapstructure:"max_size_bytes" yaml:"max_size_bytes"`
	MaxZoneSizeBytes      int64         `mapstructure:"max_zone_size_bytes" yaml:"max_zone_size_bytes"`
	SerialRefreshDebounce time.Duration `mapstructure:"serial_refresh_debounce" yaml:"serial_refresh_debounce"`
}

type CatalogSettings struct {
	Enabled           bool    `mapstructure:"enabled" yaml:"enabled"`
	ZoneName          string  `mapstructure:"zone_name" yaml:"zone_name"`
	PollInterval      float64 `mapstructure:"poll_interval" yaml:"poll_interval"`
	NotifyBindAddress string  `mapstructure:"notify_bind_address" yaml:"notify_bind_address"`
	NotifyUDPPort     int     `mapstructure:"notify_udp_port" yaml:"notify_udp_port"`
	NotifyTCPPort     int     `mapstructure:"notify_tcp_port" yaml:"notify_tcp_port"`
	AutoLoadZones     bool    `mapstructure:"auto_load_zones" yaml:"auto_load_zones"`
	RemoveStaleZones  bool    `mapstructure:"remove_stale_zones" yaml:"remove_stale_zones"`
}

const (
	RNDCSeedSharedDir   = "shared_dir"
	RNDCSeedInitialFile = "initial_file"
)

// RNDCSettings configures optional BIND rndc-backed zone create/delete.
type RNDCSettings struct {
	Enabled          bool             `mapstructure:"enabled" yaml:"enabled"`
	Host             string           `mapstructure:"host" yaml:"host"`
	Port             int              `mapstructure:"port" yaml:"port"`
	Algorithm        string           `mapstructure:"algorithm" yaml:"algorithm"`
	Secret           Secret           `mapstructure:"secret" yaml:"secret"`
	SecretFile       string           `mapstructure:"secret_file" yaml:"secret_file"`
	Timeout          time.Duration    `mapstructure:"timeout" yaml:"timeout"`
	View             string           `mapstructure:"view" yaml:"view"`
	ZoneSeed         RNDCZoneSeed     `mapstructure:"zone_seed" yaml:"zone_seed"`
	ZoneTemplate     RNDCZoneTemplate `mapstructure:"zone_template" yaml:"zone_template"`
	ZoneDefaults     RNDCZoneDefaults `mapstructure:"zone_defaults" yaml:"zone_defaults"`
	CatalogMemberTTL uint32           `mapstructure:"catalog_member_ttl" yaml:"catalog_member_ttl"`
	ReadyTimeout     time.Duration    `mapstructure:"ready_timeout" yaml:"ready_timeout"`
}

type RNDCZoneSeed struct {
	Mode        string `mapstructure:"mode" yaml:"mode"`
	LocalDir    string `mapstructure:"local_dir" yaml:"local_dir"`
	BindDir     string `mapstructure:"bind_dir" yaml:"bind_dir"`
	InitialFile string `mapstructure:"initial_file" yaml:"initial_file"`
}

type RNDCZoneTemplate struct {
	AllowUpdateKey   string `mapstructure:"allow_update_key" yaml:"allow_update_key"`
	AllowTransferKey string `mapstructure:"allow_transfer_key" yaml:"allow_transfer_key"`
	Extra            string `mapstructure:"extra" yaml:"extra"`
}

type RNDCZoneDefaults struct {
	PrimaryNS   string   `mapstructure:"primary_ns" yaml:"primary_ns"`
	AdminEmail  string   `mapstructure:"admin_email" yaml:"admin_email"`
	Nameservers []string `mapstructure:"nameservers" yaml:"nameservers"`
	TTL         uint32   `mapstructure:"ttl" yaml:"ttl"`
	Refresh     uint32   `mapstructure:"refresh" yaml:"refresh"`
	Retry       uint32   `mapstructure:"retry" yaml:"retry"`
	Expire      uint32   `mapstructure:"expire" yaml:"expire"`
	Minimum     uint32   `mapstructure:"minimum" yaml:"minimum"`
}

type NotifySettings struct {
	Enabled                bool    `mapstructure:"enabled" yaml:"enabled"`
	BindAddress            string  `mapstructure:"bind_address" yaml:"bind_address"`
	UDPPort                int     `mapstructure:"udp_port" yaml:"udp_port"`
	TCPPort                int     `mapstructure:"tcp_port" yaml:"tcp_port"`
	PreferIXFR             bool    `mapstructure:"prefer_ixfr" yaml:"prefer_ixfr"`
	RequireTSIG            bool    `mapstructure:"require_tsig" yaml:"require_tsig"`
	TSIGKey                *string `mapstructure:"tsig_key" yaml:"tsig_key"`
	RefreshCooldownSeconds float64 `mapstructure:"refresh_cooldown_seconds" yaml:"refresh_cooldown_seconds"`
}

type SchedulerSettings struct {
	Enabled             bool          `mapstructure:"enabled" yaml:"enabled"`
	DatabasePath        string        `mapstructure:"database_path" yaml:"database_path"`
	PollInterval        time.Duration `mapstructure:"poll_interval" yaml:"poll_interval"`
	MaxAttempts         int           `mapstructure:"max_attempts" yaml:"max_attempts"`
	RetryBackoff        time.Duration `mapstructure:"retry_backoff" yaml:"retry_backoff"`
	LeaseTTL            time.Duration `mapstructure:"lease_ttl" yaml:"lease_ttl"`
	DefaultExpiryWindow time.Duration `mapstructure:"default_expiry_window" yaml:"default_expiry_window"`
}

type DatabaseSettings struct {
	Backend     string            `mapstructure:"backend" yaml:"backend"`
	AutoMigrate bool              `mapstructure:"auto_migrate" yaml:"auto_migrate"`
	Path        string            `mapstructure:"path" yaml:"path"`
	Postgres    *PostgresSettings `mapstructure:"postgres" yaml:"postgres"`
}

type PostgresSettings struct {
	Host               string        `mapstructure:"host" yaml:"host"`
	Port               int           `mapstructure:"port" yaml:"port"`
	Database           string        `mapstructure:"database" yaml:"database"`
	User               string        `mapstructure:"user" yaml:"user"`
	Password           Secret        `mapstructure:"password" yaml:"password"`
	PasswordFile       string        `mapstructure:"password_file" yaml:"password_file"`
	DSN                Secret        `mapstructure:"dsn" yaml:"dsn"`
	DSNFile            string        `mapstructure:"dsn_file" yaml:"dsn_file"`
	SSLMode            string        `mapstructure:"sslmode" yaml:"sslmode"`
	Schema             string        `mapstructure:"schema" yaml:"schema"`
	PoolSize           int           `mapstructure:"pool_size" yaml:"pool_size"`
	ConnectTimeout     time.Duration `mapstructure:"connect_timeout" yaml:"connect_timeout"`
	StatementTimeoutMS int           `mapstructure:"statement_timeout_ms" yaml:"statement_timeout_ms"`
}

type RetentionSettings struct {
	Enabled       bool          `mapstructure:"enabled" yaml:"enabled"`
	Interval      time.Duration `mapstructure:"interval" yaml:"interval"`
	MaxAgeDays    int           `mapstructure:"max_age_days" yaml:"max_age_days"`
	MaxDatabaseMB int           `mapstructure:"max_database_mb" yaml:"max_database_mb"`
	TrimPercent   int           `mapstructure:"trim_percent" yaml:"trim_percent"`
	MaxTrimPasses int           `mapstructure:"max_trim_passes" yaml:"max_trim_passes"`
	Statuses      []string      `mapstructure:"statuses" yaml:"statuses"`
	Vacuum        string        `mapstructure:"vacuum" yaml:"vacuum"`
	DryRun        bool          `mapstructure:"dry_run" yaml:"dry_run"`
}

type WebhookSettings struct {
	Enabled                 bool            `mapstructure:"enabled" yaml:"enabled"`
	BaseURL                 string          `mapstructure:"base_url" yaml:"base_url"`
	Timeout                 time.Duration   `mapstructure:"timeout" yaml:"timeout"`
	MaxRetries              int             `mapstructure:"max_retries" yaml:"max_retries"`
	RetryBackoff            time.Duration   `mapstructure:"retry_backoff" yaml:"retry_backoff"`
	QueueSize               int             `mapstructure:"queue_size" yaml:"queue_size"`
	Events                  []string        `mapstructure:"events" yaml:"events"`
	Targets                 []WebhookTarget `mapstructure:"targets" yaml:"targets"`
	AutorecordManualChanges bool            `mapstructure:"autorecord_manual_changes" yaml:"autorecord_manual_changes"`
}

type WebhookAuth struct {
	Type            string `mapstructure:"type" yaml:"type"`
	Token           Secret `mapstructure:"token" yaml:"token"`
	TokenFile       string `mapstructure:"token_file" yaml:"token_file"`
	Secret          Secret `mapstructure:"secret" yaml:"secret"`
	SecretFile      string `mapstructure:"secret_file" yaml:"secret_file"`
	Username        string `mapstructure:"username" yaml:"username"`
	Password        Secret `mapstructure:"password" yaml:"password"`
	PasswordFile    string `mapstructure:"password_file" yaml:"password_file"`
	Header          string `mapstructure:"header" yaml:"header"`
	Algorithm       string `mapstructure:"algorithm" yaml:"algorithm"`
	TimestampHeader string `mapstructure:"timestamp_header" yaml:"timestamp_header"`
}

type WebhookTarget struct {
	Name           string            `mapstructure:"name" yaml:"name"`
	Format         string            `mapstructure:"type" yaml:"type"`
	URL            Secret            `mapstructure:"url" yaml:"url"`
	URLFile        string            `mapstructure:"url_file" yaml:"url_file"`
	Events         []string          `mapstructure:"events" yaml:"events"`
	Zones          []string          `mapstructure:"zones" yaml:"zones"`
	Headers        map[string]string `mapstructure:"headers" yaml:"headers"`
	Auth           WebhookAuth       `mapstructure:"auth" yaml:"auth"`
	VerifyTLS      any               `mapstructure:"verify_tls" yaml:"verify_tls"`
	AllowInsecure  bool              `mapstructure:"allow_insecure" yaml:"allow_insecure"`
	TimeoutSeconds *float64          `mapstructure:"timeout" yaml:"timeout"`
}

type ThemeSettings struct {
	AppName         *string      `mapstructure:"app_name" yaml:"app_name"`
	DefaultMode     string       `mapstructure:"default_mode" yaml:"default_mode"`
	AllowModeToggle bool         `mapstructure:"allow_mode_toggle" yaml:"allow_mode_toggle"`
	Logo            *ThemeLogo   `mapstructure:"logo" yaml:"logo"`
	Light           ThemePalette `mapstructure:"light" yaml:"light"`
	Dark            ThemePalette `mapstructure:"dark" yaml:"dark"`
}

type ThemeLogo struct {
	URL  *string `mapstructure:"url" yaml:"url"`
	Path *string `mapstructure:"path" yaml:"path"`
	Alt  string  `mapstructure:"alt" yaml:"alt"`
}

type ThemePalette struct {
	BgPrimary     *string `mapstructure:"bg_primary" yaml:"bg_primary"`
	BgSecondary   *string `mapstructure:"bg_secondary" yaml:"bg_secondary"`
	BgTertiary    *string `mapstructure:"bg_tertiary" yaml:"bg_tertiary"`
	BgHover       *string `mapstructure:"bg_hover" yaml:"bg_hover"`
	BorderColor   *string `mapstructure:"border_color" yaml:"border_color"`
	TextPrimary   *string `mapstructure:"text_primary" yaml:"text_primary"`
	TextSecondary *string `mapstructure:"text_secondary" yaml:"text_secondary"`
	TextMuted     *string `mapstructure:"text_muted" yaml:"text_muted"`
	AccentPrimary *string `mapstructure:"accent_primary" yaml:"accent_primary"`
	AccentSuccess *string `mapstructure:"accent_success" yaml:"accent_success"`
	AccentWarning *string `mapstructure:"accent_warning" yaml:"accent_warning"`
	AccentDanger  *string `mapstructure:"accent_danger" yaml:"accent_danger"`
	AccentInfo    *string `mapstructure:"accent_info" yaml:"accent_info"`
	RecordA       *string `mapstructure:"record_a" yaml:"record_a"`
	RecordAAAA    *string `mapstructure:"record_aaaa" yaml:"record_aaaa"`
	RecordCNAME   *string `mapstructure:"record_cname" yaml:"record_cname"`
	RecordMX      *string `mapstructure:"record_mx" yaml:"record_mx"`
	RecordTXT     *string `mapstructure:"record_txt" yaml:"record_txt"`
	RecordNS      *string `mapstructure:"record_ns" yaml:"record_ns"`
	RecordSOA     *string `mapstructure:"record_soa" yaml:"record_soa"`
	RecordPTR     *string `mapstructure:"record_ptr" yaml:"record_ptr"`
	RecordSRV     *string `mapstructure:"record_srv" yaml:"record_srv"`
	RecordCAA     *string `mapstructure:"record_caa" yaml:"record_caa"`
}

type LoggingSettings struct {
	Format          string  `mapstructure:"format" yaml:"format"`
	Level           string  `mapstructure:"level" yaml:"level"`
	SampleRate      float64 `mapstructure:"sample_rate" yaml:"sample_rate"`
	SlowThresholdMS int     `mapstructure:"slow_threshold_ms" yaml:"slow_threshold_ms"`
	OTLPEndpoint    *string `mapstructure:"otlp_endpoint" yaml:"otlp_endpoint"`
}

type MetricsSettings struct {
	PerZoneLabels bool `mapstructure:"per_zone_labels" yaml:"per_zone_labels"`
}
