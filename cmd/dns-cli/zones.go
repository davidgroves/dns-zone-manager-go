package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/spf13/cobra"
)

func newZoneCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "zone",
		Short: "Create or delete zones via rndc",
	}
	cmd.AddCommand(newZoneCreateCmd(opts), newZoneDeleteCmd(opts))
	return cmd
}

func newZoneCreateCmd(opts *cliOptions) *cobra.Command {
	var (
		primaryNS, adminEmail, at string
		nameservers               []string
		ttl                       uint32
		noCatalog                 bool
	)
	cmd := &cobra.Command{
		Use:   "create ZONE",
		Short: "Create a zone via rndc addzone",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			payload := map[string]any{"zone": args[0]}
			if primaryNS != "" {
				payload["primary_ns"] = primaryNS
			}
			if adminEmail != "" {
				payload["admin_email"] = adminEmail
			}
			if len(nameservers) > 0 {
				payload["nameservers"] = nameservers
			}
			if ttl > 0 {
				payload["ttl"] = ttl
			}
			if noCatalog {
				payload["catalog"] = false
			}
			if at != "" {
				t, err := time.Parse(time.RFC3339, at)
				if err != nil {
					return fmt.Errorf("invalid --at: %w", err)
				}
				payload["scheduled_at"] = t
			}
			resp, data, err := client.do(http.MethodPost, "/v1/zones", nil, payload, "application/json")
			if err != nil {
				return err
			}
			done, err := emitResponse(opts, resp, data)
			if done {
				return err
			}
			if resp.StatusCode == http.StatusAccepted {
				return printScheduledAck(data)
			}
			var res map[string]any
			if err := json.Unmarshal(data, &res); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}
			success(fmt.Sprintf("Created zone %v", res["zone"]))
			return nil
		},
	}
	cmd.Flags().StringVar(&primaryNS, "primary-ns", "", "SOA primary nameserver")
	cmd.Flags().StringVar(&adminEmail, "admin-email", "", "SOA admin email (dotted or user@host)")
	cmd.Flags().StringSliceVar(&nameservers, "ns", nil, "NS records (repeatable)")
	cmd.Flags().Uint32Var(&ttl, "ttl", 0, "Default TTL")
	cmd.Flags().BoolVar(&noCatalog, "no-catalog", false, "Do not add the zone to the catalog")
	cmd.Flags().StringVar(&at, "at", "", "Schedule create at RFC3339 time")
	return cmd
}

func newZoneDeleteCmd(opts *cliOptions) *cobra.Command {
	var keepFiles, noCatalog bool
	var at string
	cmd := &cobra.Command{
		Use:   "delete ZONE",
		Short: "Delete a zone via rndc delzone",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			q := url.Values{}
			if keepFiles {
				q.Set("keep_files", "true")
			}
			if noCatalog {
				q.Set("catalog", "false")
			}
			if at != "" {
				if _, err := time.Parse(time.RFC3339, at); err != nil {
					return fmt.Errorf("invalid --at: %w", err)
				}
				q.Set("scheduled_at", at)
			}
			resp, data, err := client.do(http.MethodDelete, zonePath(args[0]), q, nil, "")
			if err != nil {
				return err
			}
			done, err := emitResponse(opts, resp, data)
			if done {
				return err
			}
			if resp.StatusCode == http.StatusAccepted {
				return printScheduledAck(data)
			}
			var res map[string]any
			if err := json.Unmarshal(data, &res); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}
			success(fmt.Sprintf("Deleted zone %v", res["zone"]))
			return nil
		},
	}
	cmd.Flags().BoolVar(&keepFiles, "keep-files", false, "Leave zone files on disk (no delzone -clean)")
	cmd.Flags().BoolVar(&noCatalog, "no-catalog", false, "Do not remove catalog membership")
	cmd.Flags().StringVar(&at, "at", "", "Schedule delete at RFC3339 time")
	return cmd
}

func newRNDCCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rndc",
		Short: "RNDC / zone-provisioning helpers",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show rndc provisioning status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return simpleGet(opts, "/v1/rndc/status", printRNDCStatus)
		},
	})
	return cmd
}

func printRNDCStatus(data []byte) error {
	var st map[string]any
	if err := json.Unmarshal(data, &st); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	enabled, _ := st["enabled"].(bool)
	if !enabled {
		fmt.Println("RNDC: disabled")
		return nil
	}
	fmt.Printf("RNDC: enabled  host=%v  port=%v  connected=%v  seed=%v  catalog=%v\n",
		st["host"], st["port"], st["connected"], st["seed_mode"], st["catalog_enabled"])
	return nil
}

func printScheduledAck(data []byte) error {
	var ch map[string]any
	if err := json.Unmarshal(data, &ch); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	success(fmt.Sprintf("Scheduled %v %v (%v)", ch["kind"], ch["zone"], ch["id"]))
	return nil
}
