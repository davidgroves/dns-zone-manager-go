package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func newAddCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "add ZONE NAME TTL TYPE RECORDS...",
		Short: "Add a new DNS record",
		Args:  cobra.MinimumNArgs(5),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			zone, name, ttlStr, rtype := args[0], args[1], args[2], strings.ToUpper(args[3])
			ttl, err := strconv.Atoi(ttlStr)
			if err != nil {
				return fmt.Errorf("invalid TTL: %w", err)
			}
			records := args[4:]
			payload := map[string]any{
				"name":    name,
				"type":    rtype,
				"ttl":     ttl,
				"records": records,
			}
			resp, data, err := client.do(http.MethodPost, zonePath(zone)+"/rrsets", nil, payload, "application/json")
			if err != nil {
				return err
			}
			return handleMutation(opts, resp, data, fmt.Sprintf("Added %s %s to %s", name, rtype, zone))
		},
	}
}

func newDeleteCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "delete ZONE NAME TYPE [RECORDS...]",
		Short: "Delete a DNS record or specific record values",
		Args:  cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			zone, name, rtype := args[0], args[1], strings.ToUpper(args[2])
			payload := map[string]any{
				"name": name,
				"type": rtype,
			}
			var msg string
			if len(args) > 3 {
				recs := args[3:]
				payload["records"] = recs
				msg = fmt.Sprintf("Deleted %s from %s %s in %s", strings.Join(recs, ", "), name, rtype, zone)
			} else {
				msg = fmt.Sprintf("Deleted %s %s from %s", name, rtype, zone)
			}
			resp, data, err := client.do(http.MethodDelete, zonePath(zone)+"/rrsets", nil, payload, "application/json")
			if err != nil {
				return err
			}
			return handleMutation(opts, resp, data, msg)
		},
	}
}

func newReplaceCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "replace ZONE NAME TTL TYPE RECORDS...",
		Short: "Replace an existing DNS record with new values",
		Args:  cobra.MinimumNArgs(5),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			zone, name, ttlStr, rtype := args[0], args[1], args[2], strings.ToUpper(args[3])
			ttl, err := strconv.Atoi(ttlStr)
			if err != nil {
				return fmt.Errorf("invalid TTL: %w", err)
			}
			records := args[4:]
			payload := map[string]any{
				"name":    name,
				"type":    rtype,
				"ttl":     ttl,
				"records": records,
			}
			resp, data, err := client.do(http.MethodPut, zonePath(zone)+"/rrsets", nil, payload, "application/json")
			if err != nil {
				return err
			}
			return handleMutation(opts, resp, data, fmt.Sprintf("Replaced %s %s in %s", name, rtype, zone))
		},
	}
}

func newListCmd(opts *cliOptions) *cobra.Command {
	list := &cobra.Command{
		Use:   "list",
		Short: "List zones or records",
	}
	list.AddCommand(newListZonesCmd(opts), newListRecordsCmd(opts))
	return list
}

func newListZonesCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "zones",
		Short: "List all available zones",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			type zoneInfo struct {
				Zone       string `json:"zone"`
				Serial     uint32 `json:"serial"`
				RRsetCount int    `json:"rrset_count"`
			}
			var all []zoneInfo
			cursor := ""
			const pageSize = 100
			for {
				q := url.Values{}
				q.Set("limit", strconv.Itoa(pageSize))
				if cursor != "" {
					q.Set("after", cursor)
				}
				resp, data, err := client.do(http.MethodGet, "/v1/zones", q, nil, "")
				if err != nil {
					return err
				}
				if resp.StatusCode >= 300 {
					done, err := emitResponse(opts, resp, data)
					if done {
						return err
					}
					return fmt.Errorf("failed to list zones: %w", apiError(resp.StatusCode, data))
				}
				var page struct {
					Zones      []zoneInfo `json:"zones"`
					NextCursor *string    `json:"next_cursor"`
					HasMore    bool       `json:"has_more"`
				}
				if err := json.Unmarshal(data, &page); err != nil {
					return fmt.Errorf("failed to parse response: %w", err)
				}
				all = append(all, page.Zones...)
				if page.NextCursor == nil || !page.HasMore || *page.NextCursor == "" {
					break
				}
				cursor = *page.NextCursor
			}
			if opts.outputJSON {
				out, _ := json.MarshalIndent(map[string]any{"zones": all}, "", "  ")
				fmt.Println(string(out))
				return nil
			}
			if len(all) == 0 {
				fmt.Println("No zones found")
				return nil
			}
			fmt.Printf("Found %d zone(s):\n\n", len(all))
			for _, z := range all {
				fmt.Printf("  %s\n", z.Zone)
				fmt.Printf("    Serial: %d\n", z.Serial)
				fmt.Printf("    RRsets: %d\n", z.RRsetCount)
			}
			return nil
		},
	}
}

func newListRecordsCmd(opts *cliOptions) *cobra.Command {
	var rtype, name string
	cmd := &cobra.Command{
		Use:   "records ZONE",
		Short: "List records in a zone",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			zone := args[0]
			q := url.Values{}
			if rtype != "" {
				q.Set("type", strings.ToUpper(rtype))
			}
			if name != "" {
				q.Set("name", name)
			}
			resp, data, err := client.do(http.MethodGet, zonePath(zone)+"/rrsets", q, nil, "")
			if err != nil {
				return err
			}
			done, err := emitResponse(opts, resp, data)
			if done {
				return err
			}

			var records []map[string]any
			if err := json.Unmarshal(data, &records); err != nil {
				var page struct {
					Items  []map[string]any `json:"items"`
					RRsets []map[string]any `json:"rrsets"`
				}
				if err2 := json.Unmarshal(data, &page); err2 != nil {
					return fmt.Errorf("failed to parse response: %w", err)
				}
				if len(page.Items) > 0 {
					records = page.Items
				} else {
					records = page.RRsets
				}
			}
			if len(records) == 0 {
				fmt.Printf("No records found in %s\n", zone)
				return nil
			}
			fmt.Printf("Found %d record(s) in %s:\n\n", len(records), zone)
			for _, record := range records {
				fmt.Printf("  %v\n", record["name"])
				fmt.Printf("    Type: %v\n", record["type"])
				fmt.Printf("    TTL:  %v\n", record["ttl"])
				if recs, ok := record["records"].([]any); ok {
					for _, r := range recs {
						fmt.Printf("    Data: %v\n", r)
					}
				}
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&rtype, "type", "", "Filter by record type")
	cmd.Flags().StringVar(&name, "name", "", "Filter by record name")
	return cmd
}

func newGetCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "get ZONE NAME TYPE",
		Short: "Get a specific DNS record",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			zone, name, rtype := args[0], args[1], strings.ToUpper(args[2])
			path := zonePath(zone) + "/rrsets/" + url.PathEscape(name) + "/" + url.PathEscape(rtype)
			resp, data, err := client.do(http.MethodGet, path, nil, nil, "")
			if err != nil {
				return err
			}
			done, err := emitResponse(opts, resp, data)
			if done {
				return err
			}
			var record map[string]any
			if err := json.Unmarshal(data, &record); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}
			fmt.Printf("  Name: %v\n", record["name"])
			fmt.Printf("  Type: %v\n", record["type"])
			fmt.Printf("  TTL:  %v\n", record["ttl"])
			if recs, ok := record["records"].([]any); ok {
				for _, r := range recs {
					fmt.Printf("  Data: %v\n", r)
				}
			}
			return nil
		},
	}
}

func newExportCmd(opts *cliOptions) *cobra.Command {
	var outFile string
	cmd := &cobra.Command{
		Use:   "export ZONE",
		Short: "Export a zone as a BIND master format zone file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			zone := args[0]
			if opts.verbose {
				fmt.Fprintf(os.Stderr, "GET %s/export\n", zonePath(zone))
			}
			resp, data, err := client.do(http.MethodGet, zonePath(zone)+"/export", nil, nil, "")
			if err != nil {
				return err
			}
			if resp.StatusCode >= 300 {
				return fmt.Errorf("export failed: %w", apiError(resp.StatusCode, data))
			}
			if outFile != "" {
				if err := os.WriteFile(outFile, data, 0o644); err != nil {
					return fmt.Errorf("failed to write file: %w", err)
				}
				success(fmt.Sprintf("Zone exported to %s", outFile))
				return nil
			}
			_, _ = os.Stdout.Write(data)
			return nil
		},
	}
	cmd.Flags().StringVarP(&outFile, "output-file", "o", "", "Write zone file to this path (default: stdout)")
	return cmd
}
