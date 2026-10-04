package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newNsupdateCmd(opts *cliOptions) *cobra.Command {
	var file string
	var dryRun bool
	var drafts bool
	cmd := &cobra.Command{
		Use:   "nsupdate",
		Short: "Execute nsupdate-formatted commands (or save as scheduled drafts)",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			text, err := readFileOrStdin(file, "Enter nsupdate commands (Ctrl+D to finish):")
			if err != nil {
				return err
			}
			if strings.TrimSpace(text) == "" {
				return fmt.Errorf("no nsupdate commands provided")
			}

			if drafts {
				return runNsupdateDrafts(opts, client, text)
			}

			if opts.verbose {
				_, _ = fmt.Fprintf(os.Stderr, "POST /v1/nsupdate\nInput:\n%s\n", text)
			}
			q := url.Values{}
			if dryRun {
				q.Set("dry_run", "true")
			}
			resp, data, err := client.do(http.MethodPost, "/v1/nsupdate", q, text, "text/plain")
			if err != nil {
				return err
			}
			done, err := emitResponse(opts, resp, data)
			if done {
				return err
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
				return fmt.Errorf("%d nsupdate transaction(s) failed", payload.TotalFailed)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Read nsupdate commands from file (default: stdin)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate input without executing")
	cmd.Flags().BoolVar(&drafts, "drafts", false, "Save each send transaction as a scheduled draft change")
	return cmd
}

func runNsupdateDrafts(opts *cliOptions, client *apiClient, text string) error {
	if opts.verbose {
		fmt.Fprintf(os.Stderr, "POST /v1/nsupdate/drafts\nInput:\n%s\n", text)
	}
	resp, data, err := client.do(http.MethodPost, "/v1/nsupdate/drafts", nil, text, "text/plain")
	if err != nil {
		return err
	}
	done, err := emitResponse(opts, resp, data)
	if done {
		return err
	}
	var payload struct {
		Created []map[string]any `json:"created"`
		Total   int              `json:"total"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		success("nsupdate drafts created")
		return nil //nolint:nilerr
	}
	success(fmt.Sprintf("Created %d draft scheduled change(s)", payload.Total))
	for _, ch := range payload.Created {
		fmt.Printf("  %s  %s  %s  %s\n",
			strVal(ch, "id"), strVal(ch, "name"), strVal(ch, "zone"), strVal(ch, "status"))
	}
	return nil
}
