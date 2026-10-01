// Command dns-cli talks to the DNS Zone Manager HTTP API.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/davidgroves/dns-zone-manager-go/internal/version"
)

type cliOptions struct {
	baseURL    string
	apiKey     string
	outputJSON bool
	verbose    bool
}

func main() {
	opts := &cliOptions{}

	root := &cobra.Command{
		Use:   "dns-cli",
		Short: "DNS API CLI — manage DNS records via the DNS Zone Manager API",
		Long: `DNS API CLI - Manage DNS records via the DNS API server.

Configure using environment variables:

    export DNS_API_URL=http://dns-api.example.com
    export DNS_API_KEY=your-api-key

Or pass --url and --api-key options.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Current(),
	}
	root.SetVersionTemplate("dns-cli version {{.Version}}\n")

	root.PersistentFlags().StringVar(&opts.baseURL, "url", envOr("DNS_API_URL", "http://localhost:8000"), "DNS API server URL")
	root.PersistentFlags().StringVar(&opts.apiKey, "api-key", os.Getenv("DNS_API_KEY"), "API key for authentication")
	root.PersistentFlags().BoolVar(&opts.outputJSON, "json", false, "Output raw JSON response")
	root.PersistentFlags().BoolVarP(&opts.verbose, "verbose", "v", false, "Verbose output")

	root.AddCommand(
		newAddCmd(opts),
		newDeleteCmd(opts),
		newReplaceCmd(opts),
		newNsupdateCmd(opts),
		newListCmd(opts),
		newGetCmd(opts),
		newExportCmd(opts),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "✗ %s\n", err.Error())
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type apiClient struct {
	opts    *cliOptions
	baseURL *url.URL
	client  *http.Client
}

func newAPIClient(opts *cliOptions) (*apiClient, error) {
	if strings.TrimSpace(opts.apiKey) == "" {
		return nil, fmt.Errorf("API key required. Set DNS_API_KEY or use --api-key")
	}
	base := strings.TrimRight(opts.baseURL, "/")
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("invalid --url: %w", err)
	}
	return &apiClient{
		opts:    opts,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (c *apiClient) do(method, path string, query url.Values, body any, contentType string) (*http.Response, []byte, error) {
	var bodyReader io.Reader
	var jsonBody any
	switch b := body.(type) {
	case nil:
	case []byte:
		bodyReader = bytes.NewReader(b)
	case string:
		bodyReader = strings.NewReader(b)
	case io.Reader:
		bodyReader = b
	default:
		buf, err := json.Marshal(b)
		if err != nil {
			return nil, nil, err
		}
		bodyReader = bytes.NewReader(buf)
		jsonBody = b
		if contentType == "" {
			contentType = "application/json"
		}
	}

	rel, err := url.Parse(path)
	if err != nil {
		return nil, nil, err
	}
	u := c.baseURL.ResolveReference(rel)
	if query != nil {
		u.RawQuery = query.Encode()
	}

	if c.opts.verbose {
		fmt.Fprintf(os.Stderr, "%s %s\n", method, u.String())
		if jsonBody != nil {
			if raw, err := json.MarshalIndent(jsonBody, "", "  "); err == nil {
				fmt.Fprintf(os.Stderr, "Payload: %s\n", raw)
			}
		}
	}

	req, err := http.NewRequest(method, u.String(), bodyReader)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("X-API-Key", c.opts.apiKey)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	} else if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, nil, err
	}
	return resp, data, nil
}

func zonePath(zone string) string {
	return "/v1/zones/" + url.PathEscape(zone)
}

func printJSON(data []byte) {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, data, "", "  "); err != nil {
		fmt.Println(string(data))
		return
	}
	fmt.Println(pretty.String())
}

func success(msg string) {
	fmt.Printf("✓ %s\n", msg)
}

func apiError(status int, data []byte) error {
	detail := string(data)
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err == nil {
		if d, ok := obj["detail"]; ok {
			detail = fmt.Sprint(d)
		} else if m, ok := obj["message"]; ok {
			detail = fmt.Sprint(m)
		}
	}
	return fmt.Errorf("API error (%d): %s", status, detail)
}

func handleMutation(opts *cliOptions, resp *http.Response, data []byte, successMsg string) error {
	if opts.outputJSON {
		printJSON(data)
		if resp.StatusCode >= 300 {
			os.Exit(1)
		}
		return nil
	}
	if resp.StatusCode >= 300 {
		return apiError(resp.StatusCode, data)
	}
	success(successMsg)
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err == nil {
		if rrset, ok := payload["rrset"].(map[string]any); ok {
			fmt.Printf("  Name: %v\n", rrset["name"])
			fmt.Printf("  Type: %v\n", rrset["type"])
			fmt.Printf("  TTL:  %v\n", rrset["ttl"])
			if recs, ok := rrset["records"].([]any); ok {
				parts := make([]string, len(recs))
				for i, r := range recs {
					parts[i] = fmt.Sprint(r)
				}
				fmt.Printf("  Data: %s\n", strings.Join(parts, ", "))
			}
		}
	}
	return nil
}

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

func newNsupdateCmd(opts *cliOptions) *cobra.Command {
	var file string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "nsupdate",
		Short: "Execute nsupdate-formatted commands",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			var text string
			if file != "" {
				b, err := os.ReadFile(file)
				if err != nil {
					return err
				}
				text = string(b)
			} else {
				stat, _ := os.Stdin.Stat()
				if (stat.Mode() & os.ModeCharDevice) != 0 {
					fmt.Fprintln(os.Stderr, "Enter nsupdate commands (Ctrl+D to finish):")
				}
				b, err := io.ReadAll(os.Stdin)
				if err != nil {
					return err
				}
				text = string(b)
			}
			if strings.TrimSpace(text) == "" {
				return fmt.Errorf("no nsupdate commands provided")
			}
			if opts.verbose {
				fmt.Fprintf(os.Stderr, "POST /v1/nsupdate\nInput:\n%s\n", text)
			}
			q := url.Values{}
			if dryRun {
				q.Set("dry_run", "true")
			}
			resp, data, err := client.do(http.MethodPost, "/v1/nsupdate", q, text, "text/plain")
			if err != nil {
				return err
			}
			if opts.outputJSON {
				printJSON(data)
				if resp.StatusCode >= 300 {
					os.Exit(1)
				}
				return nil
			}
			if resp.StatusCode >= 300 {
				return apiError(resp.StatusCode, data)
			}
			var payload struct {
				TotalSuccess int `json:"total_success"`
				TotalFailed  int `json:"total_failed"`
				Transactions []struct {
					Zone    string `json:"zone"`
					Success bool   `json:"success"`
					Message string `json:"message"`
				} `json:"transactions"`
			}
			parseErr := json.Unmarshal(data, &payload)
			if parseErr != nil {
				// Response body may be non-JSON; the update itself already succeeded.
				success("nsupdate commands executed")
				return nil //nolint:nilerr // intentional: ignore body parse after success
			}
			if dryRun {
				success(fmt.Sprintf("Dry run: %d transaction(s) valid", len(payload.Transactions)))
			} else if payload.TotalFailed == 0 {
				success(fmt.Sprintf("%d transaction(s) completed successfully", payload.TotalSuccess))
			} else {
				fmt.Printf("⚠ %d succeeded, %d failed\n", payload.TotalSuccess, payload.TotalFailed)
			}
			for _, tx := range payload.Transactions {
				icon := "✓"
				if !tx.Success {
					icon = "✗"
				}
				fmt.Printf("  %s %s: %s\n", icon, tx.Zone, tx.Message)
			}
			if payload.TotalFailed > 0 {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Read nsupdate commands from file (default: stdin)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate input without executing")
	return cmd
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
					if opts.outputJSON {
						printJSON(data)
						os.Exit(1)
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
			if opts.outputJSON {
				printJSON(data)
				if resp.StatusCode >= 300 {
					os.Exit(1)
				}
				return nil
			}
			if resp.StatusCode >= 300 {
				return fmt.Errorf("failed to list records: %w", apiError(resp.StatusCode, data))
			}

			var records []map[string]any
			if err := json.Unmarshal(data, &records); err != nil {
				// Paginated shape
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
			if opts.outputJSON {
				printJSON(data)
				if resp.StatusCode >= 300 {
					os.Exit(1)
				}
				return nil
			}
			if resp.StatusCode >= 300 {
				return fmt.Errorf("record not found: %w", apiError(resp.StatusCode, data))
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
