package perfharness

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Scenario is a file under perf/scenarios.
type Scenario struct {
	Name             string     `yaml:"name"`
	Description      string     `yaml:"description"`
	Zone             string     `yaml:"zone"`
	Provision        bool       `yaml:"provision"`
	RequireZone      bool       `yaml:"require_zone"`
	SkipGenerate     bool       `yaml:"skip_generate"`
	SkipCacheRefresh bool       `yaml:"skip_cache_refresh"`
	Preset           *string    `yaml:"preset"`
	Records          *int       `yaml:"records"`
	Seed             *int       `yaml:"seed"`
	Notes            []string   `yaml:"notes"`
	ColdReads        bool       `yaml:"cold_reads"`
	MeasureSerialLag bool       `yaml:"measure_serial_lag"`
	ColdStart        *ColdStart `yaml:"cold_start"`
	Load             *LoadSpec  `yaml:"load"`
}

// ColdStart is the optional restart-and-time block.
type ColdStart struct {
	Restart       bool    `yaml:"restart"`
	HealthTimeout float64 `yaml:"health_timeout"`
	ListTimeout   float64 `yaml:"list_timeout"`
}

// LoadSpec is the scenario load block.
type LoadSpec struct {
	Mode           string     `yaml:"mode"`
	Concurrency    int        `yaml:"concurrency"`
	PoolSize       int        `yaml:"pool_size"`
	Readers        bool       `yaml:"readers"`
	ReaderRPS      float64    `yaml:"reader_rps"`
	WebsocketCount int        `yaml:"websocket_count"`
	Zones          []string   `yaml:"zones"`
	Steps          []StepSpec `yaml:"steps"`
	RPS            float64    `yaml:"rps"`
	Duration       float64    `yaml:"duration"`
}

// ListScenarios returns scenario names, sorted.
func ListScenarios() ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(ScenariosDir(), "*.yaml"))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, strings.TrimSuffix(filepath.Base(m), ".yaml"))
	}
	sort.Strings(names)
	return names, nil
}

// LoadScenario reads a name, "all" is not handled here, or a YAML path.
func LoadScenario(name string) (Scenario, error) {
	path := filepath.Join(ScenariosDir(), name+".yaml")
	if _, err := os.Stat(path); err != nil {
		if _, err2 := os.Stat(name); err2 == nil {
			path = name
		} else {
			return Scenario{}, fmt.Errorf("scenario not found: %s (looked in %s)", name, ScenariosDir())
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, err
	}
	var sc Scenario
	if err := yaml.Unmarshal(b, &sc); err != nil {
		return Scenario{}, err
	}
	if sc.Name == "" {
		sc.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return sc, nil
}

// RunOverrides are CLI overrides for a scenario run.
type RunOverrides struct {
	Records    int
	Preset     string
	RPS        []float64
	LowestRPS  float64
	HighestRPS float64
	StepRPS    float64
	Duration   float64
}

// BuildRPSRange returns inclusive rates from lowest to highest by step.
func BuildRPSRange(lowest, highest, step float64) ([]float64, error) {
	if lowest <= 0 || highest <= 0 || step <= 0 {
		return nil, fmt.Errorf("--lowest-rps, --highest-rps, and --step-rps must all be > 0")
	}
	if highest < lowest {
		return nil, fmt.Errorf("--highest-rps (%g) must be >= --lowest-rps (%g)", highest, lowest)
	}
	n := int(math.Floor((highest-lowest)/step+1e-9)) + 1
	if n > 500 {
		return nil, fmt.Errorf("rate sweep would produce %d steps (max 500); widen --step-rps", n)
	}
	out := make([]float64, 0, n)
	for i := 0; ; i++ {
		rate := lowest + float64(i)*step
		if rate > highest+step*1e-9 {
			break
		}
		if rate > highest {
			rate = highest
		}
		out = append(out, rate)
		if rate >= highest-1e-9 {
			break
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("rate sweep produced no steps")
	}
	last := out[len(out)-1]
	if math.Abs(last-highest) > 1e-6 {
		out = append(out, highest)
	}
	return out, nil
}

func floatSlicesEqual(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Abs(a[i]-b[i]) > 1e-6 {
			return false
		}
	}
	return true
}

// ResolveRateOverrides expands --lowest-rps/--highest-rps/--step-rps into RPS.
func ResolveRateOverrides(ov *RunOverrides) error {
	hasRange := ov.LowestRPS > 0 || ov.HighestRPS > 0 || ov.StepRPS > 0
	if !hasRange {
		return nil
	}
	rates, err := BuildRPSRange(ov.LowestRPS, ov.HighestRPS, ov.StepRPS)
	if err != nil {
		return err
	}
	if len(ov.RPS) > 0 && !floatSlicesEqual(ov.RPS, rates) {
		return fmt.Errorf("--rps cannot be combined with --lowest-rps/--highest-rps/--step-rps")
	}
	ov.RPS = rates
	return nil
}

// ApplyLoadRateOverride replaces scenario load steps with ov.RPS.
func ApplyLoadRateOverride(sc *Scenario, ov RunOverrides) error {
	if len(ov.RPS) == 0 {
		return nil
	}
	if sc.Load == nil {
		return fmt.Errorf("--rps requires a scenario with a load block")
	}
	for _, rate := range ov.RPS {
		if rate <= 0 {
			return fmt.Errorf("--rps values must be > 0 (got %g)", rate)
		}
	}
	baseDur := ov.Duration
	if baseDur <= 0 {
		switch {
		case len(sc.Load.Steps) > 0 && sc.Load.Steps[0].Duration > 0:
			baseDur = sc.Load.Steps[0].Duration
		case sc.Load.Duration > 0:
			baseDur = sc.Load.Duration
		default:
			baseDur = 60
		}
	}
	steps := make([]StepSpec, len(ov.RPS))
	for i, rate := range ov.RPS {
		steps[i] = StepSpec{RPS: rate, Duration: baseDur}
	}
	sc.Load.Steps = steps
	sc.Load.RPS = 0
	sc.Load.Duration = 0
	return nil
}

// RunScenario executes one scenario and returns the report. It does not write files.
func RunScenario(cfg Config, sc Scenario, ov RunOverrides) Report {
	zone := sc.Zone
	if zone == "" {
		zone = DefaultZone
	}
	zones := TargetZones(zone, nil)
	if sc.Load != nil {
		zones = TargetZones(zone, sc.Load.Zones)
	}
	started := time.Now().UTC().Format(time.RFC3339Nano)
	var errors []string
	notes := append([]string{}, sc.Notes...)
	var provision map[string]any
	var steps []StepResult

	if err := ResolveRateOverrides(&ov); err != nil {
		errors = append(errors, err.Error())
	} else if err := ApplyLoadRateOverride(&sc, ov); err != nil {
		errors = append(errors, err.Error())
	}

	if sc.Provision {
		count, err := recordsFor(sc, ov.Records, ov.Preset)
		if err != nil {
			errors = append(errors, err.Error())
		} else {
			seed := 42
			if sc.Seed != nil {
				seed = *sc.Seed
			}
			prov := CreateZone(cfg, zone, count, seed, sc.SkipGenerate, sc.SkipCacheRefresh)
			provision = prov.asMap()
			errors = append(errors, prov.Errors...)
		}
	}

	if sc.RequireZone && !sc.Provision {
		for _, candidate := range zones {
			if _, ok := QuerySOA(cfg.BindHost, cfg.BindPort, candidate); !ok {
				errors = append(errors, fmt.Sprintf("Zone %s not loaded in BIND; run: ./perf.sh zone create --zone %s", candidate, candidate))
			}
		}
	}

	probeBefore := EnvironmentProbe(cfg, zone)
	metricsBefore, err := snapshotMetrics(cfg)
	if err != nil {
		errors = append(errors, "metrics before failed: "+err.Error())
		metricsBefore = nil
	}

	blocked := false
	for _, e := range errors {
		if strings.Contains(e, "not loaded") || strings.Contains(e, "--rps") || strings.Contains(e, "--lowest-rps") {
			blocked = true
			break
		}
	}

	if sc.ColdReads && !blocked {
		steps = append(steps, StepResult{
			Label: "cold-reads", Writers: map[string]any{},
			Readers: coldReads(cfg, zone),
		})
	}

	if sc.ColdStart != nil && !blocked {
		steps = append(steps, StepResult{
			Label: "cold-start", Writers: coldStart(cfg, zone, *sc.ColdStart),
		})
	}

	if sc.Load != nil && !blocked {
		opt := loadOptions(*sc.Load)
		if len(opt.Steps) == 0 {
			rps := optRPS(sc.Load)
			dur := sc.Load.Duration
			if dur <= 0 {
				dur = 60
			}
			opt.Steps = []StepSpec{{RPS: rps, Duration: dur}}
		}
		steps = append(steps, RunRamp(cfg, zone, opt, true)...)
	}

	probeAfter := EnvironmentProbe(cfg, zone)
	var delta map[string]float64
	if metricsAfter, err := snapshotMetrics(cfg); err != nil {
		errors = append(errors, "metrics after failed: "+err.Error())
	} else if metricsBefore != nil {
		delta = MetricsDelta(metricsBefore, metricsAfter)
	}
	if sc.MeasureSerialLag {
		lag := MeasureSerialLag(cfg, zone, 30*time.Second)
		notes = append(notes, "serial_lag="+compactJSON(lag))
		probeAfter["serial_lag"] = lag
	}

	cfgMap := map[string]any{
		"zone":     zone,
		"api_base": cfg.APIBase,
		"bind":     fmt.Sprintf("%s:%d", cfg.BindHost, cfg.BindPort),
	}
	if sc.Load != nil && len(sc.Load.Zones) > 0 {
		cfgMap["zones"] = len(TargetZones(zone, sc.Load.Zones))
	} else {
		cfgMap["zones"] = 1
	}
	if len(ov.RPS) > 0 {
		cfgMap["rps"] = ov.RPS
		if ov.Duration > 0 {
			cfgMap["duration"] = ov.Duration
		}
	}
	if ov.LowestRPS > 0 && ov.HighestRPS > 0 && ov.StepRPS > 0 {
		cfgMap["lowest_rps"] = ov.LowestRPS
		cfgMap["highest_rps"] = ov.HighestRPS
		cfgMap["step_rps"] = ov.StepRPS
	}

	return Report{
		Scenario:     sc.Name,
		StartedAt:    started,
		FinishedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		Host:         hostInfo(),
		Config:       cfgMap,
		Steps:        steps,
		Provision:    provision,
		Probes:       map[string]any{"before": probeBefore, "after": probeAfter},
		MetricsDelta: delta,
		Notes:        notes,
		Errors:       errors,
	}
}

func recordsFor(sc Scenario, override int, preset string) (int, error) {
	if override > 0 {
		return override, nil
	}
	rec := 0
	if sc.Records != nil {
		rec = *sc.Records
	}
	if preset == "" && sc.Preset != nil {
		preset = *sc.Preset
	}
	if rec > 0 && preset == "" {
		return rec, nil
	}
	return ResolveRecords(preset, rec)
}

func loadOptions(spec LoadSpec) LoadOptions {
	mode := spec.Mode
	if mode == "" {
		mode = "api"
	}
	conc := spec.Concurrency
	if conc == 0 {
		conc = 16
	}
	pool := spec.PoolSize
	if pool == 0 {
		pool = 1000
	}
	reader := spec.ReaderRPS
	if reader == 0 {
		reader = 5
	}
	return LoadOptions{
		Mode: mode, Concurrency: conc, PoolSize: pool,
		Readers: spec.Readers, ReaderRPS: reader, WebsocketCount: spec.WebsocketCount,
		Zones: spec.Zones, Steps: spec.Steps,
	}
}

func optRPS(spec *LoadSpec) float64 {
	if spec != nil && spec.RPS > 0 {
		return spec.RPS
	}
	return 100
}

func coldReads(cfg Config, zone string) map[string]any {
	kinds := []string{"first_page", "deep_page", "search", "zone_list", "export"}
	client := &http.Client{Timeout: 600 * time.Second}
	base := strings.TrimRight(cfg.APIBase, "/")
	zp := urlPath(zone)
	out := map[string]any{}
	for _, kind := range kinds {
		st := &Latency{}
		t0 := time.Now()
		err := readerOnce(client, cfg, base, zp, "host0001000", kind)
		st.Record(float64(time.Since(t0).Microseconds())/1000.0, errKind(err))
		out[kind] = st.Summary()
	}
	return out
}

func coldStart(cfg Config, zone string, cs ColdStart) map[string]any {
	result := map[string]any{"restarted": false}
	container := os.Getenv("PERF_APP_CONTAINER")
	if container == "" {
		container = "dns-zone-manager_devcontainer-dev-1"
	}
	if cs.Restart {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "docker", "restart", container)
		if out, err := cmd.CombinedOutput(); err != nil {
			result["restart_error"] = strings.TrimSpace(string(out) + " " + err.Error())
			result["note"] = "Could not docker-restart; restart the app manually and re-run, or set PERF_APP_CONTAINER."
		} else {
			result["restarted"] = true
			result["container"] = container
		}
	}
	healthTimeout := cs.HealthTimeout
	if healthTimeout <= 0 {
		healthTimeout = 120
	}
	listTimeout := cs.ListTimeout
	if listTimeout <= 0 {
		listTimeout = 600
	}
	t0 := time.Now()
	deadline := t0.Add(time.Duration(healthTimeout * float64(time.Second)))
	healthOK := false
	for time.Now().Before(deadline) {
		if ok, _ := healthOKNow(cfg); ok {
			healthOK = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	result["health_ok"] = healthOK
	result["health_seconds"] = time.Since(t0).Seconds()

	t1 := time.Now()
	clientTimeout := time.Duration(listTimeout * float64(time.Second))
	err := readerOnce(&http.Client{Timeout: clientTimeout}, cfg, strings.TrimRight(cfg.APIBase, "/"), urlPath(zone), "host0001000", "first_page")
	result["list_ok"] = err == nil
	if err != nil {
		result["list_error"] = err.Error()
	}
	result["first_list_seconds"] = time.Since(t1).Seconds()
	return result
}
