package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func newSearchCmd(opts *cliOptions) *cobra.Command {
	var zone, name, value, rtype string
	cmd := &cobra.Command{
		Use:   "search",
		Short: "Search records by name/value pattern",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" && value == "" {
				return fmt.Errorf("--name or --value is required")
			}
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			q := url.Values{}
			if name != "" {
				q.Set("name_pattern", name)
			}
			if value != "" {
				q.Set("value_pattern", value)
			}
			if rtype != "" {
				q.Set("type", strings.ToUpper(rtype))
			}
			path := "/v1/search"
			if zone != "" {
				path = zonePath(zone) + "/search"
			}
			resp, data, err := client.do(http.MethodGet, path, q, nil, "")
			if err != nil {
				return err
			}
			done, err := emitResponse(opts, resp, data)
			if done {
				return err
			}
			if zone != "" {
				return printZoneSearch(data, zone)
			}
			return printGlobalSearch(data)
		},
	}
	cmd.Flags().StringVar(&zone, "zone", "", "Limit search to a single zone")
	cmd.Flags().StringVar(&name, "name", "", "Name regex pattern")
	cmd.Flags().StringVar(&value, "value", "", "Value regex pattern")
	cmd.Flags().StringVar(&rtype, "type", "", "Filter by record type")
	return cmd
}

func printZoneSearch(data []byte, zone string) error {
	var page struct {
		Results    []map[string]any `json:"results"`
		TotalCount int              `json:"total_count"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if len(page.Results) == 0 {
		fmt.Printf("No matches in %s\n", zone)
		return nil
	}
	fmt.Printf("Found %d match(es) in %s:\n\n", page.TotalCount, zone)
	for _, r := range page.Results {
		fmt.Printf("  %v  %v  ttl=%v  %v\n", r["name"], r["type"], r["ttl"], r["records"])
	}
	return nil
}

func printGlobalSearch(data []byte) error {
	var page struct {
		Results    []map[string]any `json:"results"`
		TotalCount int              `json:"total_count"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if len(page.Results) == 0 {
		fmt.Println("No matches")
		return nil
	}
	fmt.Printf("Found matches across %d zone group(s) (total_count=%d):\n\n", len(page.Results), page.TotalCount)
	for _, zr := range page.Results {
		fmt.Printf("  Zone: %v (serial %v)\n", zr["zone"], zr["serial"])
		if rrsets, ok := zr["rrsets"].([]any); ok {
			for _, rr := range rrsets {
				rm, _ := rr.(map[string]any)
				fmt.Printf("    %v  %v  ttl=%v  %v\n", rm["name"], rm["type"], rm["ttl"], rm["records"])
			}
		}
	}
	return nil
}

func newCatalogCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Catalog zone status and sync",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "status",
			Short: "Show catalog status",
			RunE: func(cmd *cobra.Command, args []string) error {
				return simpleGet(opts, "/v1/catalog/status", printCatalogStatus)
			},
		},
		&cobra.Command{
			Use:   "zones",
			Short: "List catalog-discovered zones",
			RunE: func(cmd *cobra.Command, args []string) error {
				return simpleGet(opts, "/v1/catalog/zones", printCatalogZones)
			},
		},
		&cobra.Command{
			Use:   "sync",
			Short: "Sync zones from the catalog",
			RunE: func(cmd *cobra.Command, args []string) error {
				client, err := newAPIClient(opts)
				if err != nil {
					return err
				}
				resp, data, err := client.do(http.MethodPost, "/v1/catalog/sync", nil, nil, "")
				if err != nil {
					return err
				}
				done, err := emitResponse(opts, resp, data)
				if done {
					return err
				}
				var body map[string]any
				if err := json.Unmarshal(data, &body); err != nil {
					success("Catalog sync completed")
					return nil //nolint:nilerr
				}
				success(fmt.Sprintf("Catalog sync: %v", body))
				return nil
			},
		},
	)
	return cmd
}

func printCatalogStatus(data []byte) error {
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Printf("  Enabled:    %v\n", body["enabled"])
	fmt.Printf("  Connected:  %v\n", body["connected"])
	if zn := strVal(body, "zone_name"); zn != "" {
		fmt.Printf("  Zone:       %s\n", zn)
	}
	if v, ok := body["zones_discovered"]; ok {
		fmt.Printf("  Discovered: %v\n", v)
	}
	return nil
}

func printCatalogZones(data []byte) error {
	var body struct {
		Zones []map[string]any `json:"zones"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if len(body.Zones) == 0 {
		fmt.Println("No catalog zones")
		return nil
	}
	fmt.Printf("Found %d zone(s):\n\n", body.Total)
	for _, z := range body.Zones {
		fmt.Printf("  %s  catalog=%v  loaded=%v\n", strVal(z, "zone"), z["from_catalog"], z["loaded"])
	}
	return nil
}

func simpleGet(opts *cliOptions, path string, printer func([]byte) error) error {
	client, err := newAPIClient(opts)
	if err != nil {
		return err
	}
	resp, data, err := client.do(http.MethodGet, path, nil, nil, "")
	if err != nil {
		return err
	}
	done, err := emitResponse(opts, resp, data)
	if done {
		return err
	}
	return printer(data)
}

func newRefreshCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "refresh ZONE",
		Short: "Refresh a zone from the DNS server into the cache",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			zone := args[0]
			resp, data, err := client.do(http.MethodPost, zonePath(zone)+"/refresh", nil, nil, "")
			if err != nil {
				return err
			}
			done, err := emitResponse(opts, resp, data)
			if done {
				return err
			}
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				success(fmt.Sprintf("Refreshed %s", zone))
				return nil //nolint:nilerr
			}
			success(fmt.Sprintf("Refreshed %s (serial %v, %v rrsets)", zone, body["serial"], body["rrset_count"]))
			return nil
		},
	}
}

func newCacheInvalidateCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "cache-invalidate ZONE",
		Short: "Invalidate the in-memory cache for a zone",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			zone := args[0]
			resp, data, err := client.do(http.MethodDelete, zonePath(zone)+"/cache", nil, nil, "")
			if err != nil {
				return err
			}
			if opts.outputJSON {
				if len(data) == 0 {
					fmt.Println("{}")
				} else {
					printJSON(data)
				}
				if resp.StatusCode >= 300 {
					return apiError(resp.StatusCode, data)
				}
				return nil
			}
			if resp.StatusCode >= 300 {
				return apiError(resp.StatusCode, data)
			}
			success(fmt.Sprintf("Invalidated cache for %s", zone))
			return nil
		},
	}
}

func newAtomicCmd(opts *cliOptions) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "atomic ZONE",
		Short: "Apply an atomic multi-operation update from a JSON file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return fmt.Errorf("--file is required")
			}
			payload, err := loadJSONFile(file)
			if err != nil {
				return err
			}
			if _, ok := payload["operations"]; !ok {
				return fmt.Errorf("operations is required in JSON file")
			}
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			zone := args[0]
			resp, data, err := client.do(http.MethodPost, zonePath(zone)+"/atomic", nil, payload, "application/json")
			if err != nil {
				return err
			}
			done, err := emitResponse(opts, resp, data)
			if done {
				return err
			}
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				success(fmt.Sprintf("Atomic update applied to %s", zone))
				return nil //nolint:nilerr
			}
			success(fmt.Sprintf("Atomic update applied to %s", zone))
			if msg := strVal(body, "message"); msg != "" {
				fmt.Printf("  %s\n", msg)
			}
			if serial := strVal(body, "new_serial"); serial != "" {
				fmt.Printf("  New serial: %s\n", serial)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "JSON body with operations[]")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newHistoryCmd(opts *cliOptions) *cobra.Command {
	var fromSerial int64
	cmd := &cobra.Command{
		Use:   "history ZONE",
		Short: "Show zone history via IXFR",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			zone := args[0]
			q := url.Values{}
			if cmd.Flags().Changed("from-serial") {
				q.Set("from_serial", strconv.FormatInt(fromSerial, 10))
			}
			resp, data, err := client.do(http.MethodGet, zonePath(zone)+"/history", q, nil, "")
			if err != nil {
				return err
			}
			done, err := emitResponse(opts, resp, data)
			if done {
				return err
			}
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}
			fmt.Printf("History for %s\n", zone)
			fmt.Printf("  Current serial: %v\n", body["current_serial"])
			fmt.Printf("  Available from: %v\n", body["available_from_serial"])
			if hist, ok := body["history"].([]any); ok {
				fmt.Printf("  Batches: %d\n", len(hist))
				for i, batch := range hist {
					bm, _ := batch.(map[string]any)
					fmt.Printf("    [%d] serial %v → %v (%v changes)\n",
						i, bm["from_serial"], bm["to_serial"], bm["change_count"])
				}
			}
			return nil
		},
	}
	cmd.Flags().Int64Var(&fromSerial, "from-serial", -1, "Start history from this serial")
	return cmd
}

func newRollbackCmd(opts *cliOptions) *cobra.Command {
	var toSerial uint32
	var preview bool
	cmd := &cobra.Command{
		Use:   "rollback ZONE",
		Short: "Rollback a zone to a previous serial",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("to-serial") {
				return fmt.Errorf("--to-serial is required")
			}
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			zone := args[0]
			if preview {
				q := url.Values{}
				q.Set("target_serial", strconv.FormatUint(uint64(toSerial), 10))
				resp, data, err := client.do(http.MethodGet, zonePath(zone)+"/history/rollback/preview", q, nil, "")
				if err != nil {
					return err
				}
				done, err := emitResponse(opts, resp, data)
				if done {
					return err
				}
				var body map[string]any
				if err := json.Unmarshal(data, &body); err != nil {
					return fmt.Errorf("failed to parse response: %w", err)
				}
				fmt.Printf("Rollback preview for %s → serial %d\n", zone, toSerial)
				fmt.Printf("  Current: %v  Can rollback: %v  Changes: %v\n",
					body["current_serial"], body["can_rollback"], body["change_count"])
				if w := strVal(body, "warning"); w != "" {
					fmt.Printf("  Warning: %s\n", w)
				}
				return nil
			}
			payload := map[string]any{"target_serial": toSerial}
			resp, data, err := client.do(http.MethodPost, zonePath(zone)+"/history/rollback", nil, payload, "application/json")
			if err != nil {
				return err
			}
			done, err := emitResponse(opts, resp, data)
			if done {
				return err
			}
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				success(fmt.Sprintf("Rolled back %s", zone))
				return nil //nolint:nilerr
			}
			success(fmt.Sprintf("Rolled back %s: %s", zone, strVal(body, "message")))
			fmt.Printf("  From serial: %v → to: %v (new: %v)\n",
				body["from_serial"], body["to_serial"], body["new_serial"])
			return nil
		},
	}
	cmd.Flags().Uint32Var(&toSerial, "to-serial", 0, "Target SOA serial")
	cmd.Flags().BoolVar(&preview, "preview", false, "Preview only; do not apply")
	_ = cmd.MarkFlagRequired("to-serial")
	return cmd
}

func newReverseCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reverse",
		Short: "Check or create reverse PTR records",
	}
	cmd.AddCommand(newReverseCheckCmd(opts), newReverseCreateCmd(opts))
	return cmd
}

func newReverseCheckCmd(opts *cliOptions) *cobra.Command {
	var name, rtype string
	cmd := &cobra.Command{
		Use:   "check IPS...",
		Short: "Check reverse PTR feasibility for IPs",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if rtype == "" {
				rtype = "A"
			}
			rtype = strings.ToUpper(rtype)
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			payload := map[string]any{
				"source_name": name,
				"source_type": rtype,
				"records":     args,
			}
			resp, data, err := client.do(http.MethodPost, "/v1/reverse-ptr/check", nil, payload, "application/json")
			if err != nil {
				return err
			}
			done, err := emitResponse(opts, resp, data)
			if done {
				return err
			}
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}
			fmt.Printf("Any can create: %v\n", body["any_can_create"])
			if results, ok := body["results"].([]any); ok {
				for _, r := range results {
					rm, _ := r.(map[string]any)
					msg := strVal(rm, "message")
					if msg == "" {
						msg = strVal(rm, "error")
					}
					fmt.Printf("  %v  can_create=%v  %v\n", rm["ip"], rm["can_create"], msg)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Forward FQDN that owns the A/AAAA")
	cmd.Flags().StringVar(&rtype, "type", "A", "Source record type (A or AAAA)")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newReverseCreateCmd(opts *cliOptions) *cobra.Command {
	var name, rtype, mode string
	var ttl uint32
	cmd := &cobra.Command{
		Use:   "create IPS...",
		Short: "Create reverse PTR records pointing at --name",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			_ = rtype // accepted for symmetry with check; create uses ptr_target
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			payload := map[string]any{
				"ptr_target": name,
				"ips":        args,
			}
			if mode != "" {
				payload["mode"] = mode
			}
			if cmd.Flags().Changed("ttl") {
				payload["ttl"] = ttl
			}
			resp, data, err := client.do(http.MethodPost, "/v1/reverse-ptr", nil, payload, "application/json")
			if err != nil {
				return err
			}
			done, err := emitResponse(opts, resp, data)
			if done {
				return err
			}
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				success("Reverse PTR create completed")
				return nil //nolint:nilerr
			}
			success(fmt.Sprintf("Reverse PTR create: created=%v skipped=%v errors=%v",
				body["created_count"], body["skipped_count"], body["error_count"]))
			if results, ok := body["results"].([]any); ok {
				for _, r := range results {
					rm, _ := r.(map[string]any)
					msg := strVal(rm, "message")
					if msg == "" {
						msg = strVal(rm, "error")
					}
					fmt.Printf("  %v  %v  %v\n", rm["ip"], rm["status"], msg)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "PTR target FQDN")
	cmd.Flags().StringVar(&rtype, "type", "A", "Source record type (informational)")
	cmd.Flags().StringVar(&mode, "mode", "", "skip_existing|replace|add_roundrobin")
	cmd.Flags().Uint32Var(&ttl, "ttl", 3600, "PTR TTL")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}
