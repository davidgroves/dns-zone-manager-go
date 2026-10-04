package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/spf13/cobra"
)

func newScheduleCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schedule",
		Short: "Manage scheduled DNS changes",
	}
	cmd.AddCommand(
		newScheduleListCmd(opts),
		newScheduleGetCmd(opts),
		newScheduleCreateCmd(opts),
		newScheduleUpdateCmd(opts),
		newScheduleCancelCmd(opts),
		newScheduleApplyCmd(opts),
		newSchedulePreviewCmd(opts),
		newScheduleRevertCmd(opts),
		newScheduleRevertPreviewCmd(opts),
		newScheduleEventsCmd(opts),
	)
	return cmd
}

func newScheduleListCmd(opts *cliOptions) *cobra.Command {
	var status, zone, source string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List scheduled changes",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			q := url.Values{}
			if status != "" {
				q.Set("status", status)
			}
			if zone != "" {
				q.Set("zone", zone)
			}
			if source != "" {
				q.Set("source", source)
			}
			resp, data, err := client.do(http.MethodGet, "/v1/scheduled-changes", q, nil, "")
			if err != nil {
				return err
			}
			done, err := emitResponse(opts, resp, data)
			if done {
				return err
			}
			var payload struct {
				Changes []map[string]any `json:"changes"`
				Total   int              `json:"total"`
			}
			if err := json.Unmarshal(data, &payload); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}
			if len(payload.Changes) == 0 {
				fmt.Println("No scheduled changes found")
				return nil
			}
			fmt.Printf("Found %d scheduled change(s):\n\n", payload.Total)
			fmt.Printf("  %-36s  %-20s  %-24s  %-12s  %s\n", "ID", "NAME", "ZONE", "STATUS", "SCHEDULED_AT")
			for _, ch := range payload.Changes {
				fmt.Printf("  %-36s  %-20s  %-24s  %-12s  %s\n",
					truncate(strVal(ch, "id"), 36),
					truncate(strVal(ch, "name"), 20),
					truncate(strVal(ch, "zone"), 24),
					strVal(ch, "status"),
					strVal(ch, "scheduled_at"),
				)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "Filter by status (draft, pending, applied, …)")
	cmd.Flags().StringVar(&zone, "zone", "", "Filter by zone")
	cmd.Flags().StringVar(&source, "source", "", "Filter by source")
	return cmd
}

func newScheduleGetCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "get ID",
		Short: "Get a scheduled change",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			id := args[0]
			resp, data, err := client.do(http.MethodGet, "/v1/scheduled-changes/"+url.PathEscape(id), nil, nil, "")
			if err != nil {
				return err
			}
			done, err := emitResponse(opts, resp, data)
			if done {
				return err
			}
			var ch map[string]any
			if err := json.Unmarshal(data, &ch); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}
			printChangeDetail(ch)
			return nil
		},
	}
}

func newScheduleCreateCmd(opts *cliOptions) *cobra.Command {
	var file, name, zone, at, until string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a scheduled change from a JSON file",
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return fmt.Errorf("--file is required")
			}
			payload, err := loadJSONFile(file)
			if err != nil {
				return err
			}
			if name != "" {
				payload["name"] = name
			}
			if zone != "" {
				payload["zone"] = zone
			}
			if at != "" {
				if _, err := time.Parse(time.RFC3339, at); err != nil {
					return fmt.Errorf("invalid --at (use RFC3339): %w", err)
				}
				payload["scheduled_at"] = at
			}
			if until != "" {
				if _, err := time.Parse(time.RFC3339, until); err != nil {
					return fmt.Errorf("invalid --until (use RFC3339): %w", err)
				}
				payload["not_valid_after"] = until
			}
			if strVal(payload, "name") == "" {
				return fmt.Errorf("name is required (in file or via --name)")
			}
			if strVal(payload, "zone") == "" {
				return fmt.Errorf("zone is required (in file or via --zone)")
			}
			if _, ok := payload["operations"]; !ok {
				return fmt.Errorf("operations is required in JSON file")
			}
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			resp, data, err := client.do(http.MethodPost, "/v1/scheduled-changes", nil, payload, "application/json")
			if err != nil {
				return err
			}
			done, err := emitResponse(opts, resp, data)
			if done {
				return err
			}
			var ch map[string]any
			if err := json.Unmarshal(data, &ch); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}
			success(fmt.Sprintf("Created scheduled change %s", strVal(ch, "id")))
			printChangeDetail(ch)
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "JSON body matching POST /v1/scheduled-changes")
	cmd.Flags().StringVar(&name, "name", "", "Override name from file")
	cmd.Flags().StringVar(&zone, "zone", "", "Override zone from file")
	cmd.Flags().StringVar(&at, "at", "", "Override scheduled_at (RFC3339)")
	cmd.Flags().StringVar(&until, "until", "", "Override not_valid_after (RFC3339)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newScheduleUpdateCmd(opts *cliOptions) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "update ID",
		Short: "Update a draft scheduled change from a JSON file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return fmt.Errorf("--file is required")
			}
			payload, err := loadJSONFile(file)
			if err != nil {
				return err
			}
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			id := args[0]
			resp, data, err := client.do(http.MethodPatch, "/v1/scheduled-changes/"+url.PathEscape(id), nil, payload, "application/json")
			if err != nil {
				return err
			}
			done, err := emitResponse(opts, resp, data)
			if done {
				return err
			}
			var ch map[string]any
			if err := json.Unmarshal(data, &ch); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}
			success(fmt.Sprintf("Updated scheduled change %s", id))
			printChangeDetail(ch)
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "JSON body matching PATCH /v1/scheduled-changes/{id}")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newScheduleCancelCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "cancel ID",
		Short: "Cancel a scheduled change",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return scheduleAction(opts, http.MethodDelete, args[0], "", nil, "Cancelled")
		},
	}
}

func newScheduleApplyCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "apply ID",
		Short: "Apply a scheduled change now",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			id := args[0]
			path := "/v1/scheduled-changes/" + url.PathEscape(id) + "/apply"
			resp, data, err := client.do(http.MethodPost, path, nil, nil, "")
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
			if ok, _ := body["success"].(bool); ok {
				success(fmt.Sprintf("Applied %s: %s", id, strVal(body, "message")))
			} else {
				fmt.Printf("✗ Apply failed for %s: %s\n", id, strVal(body, "message"))
				return fmt.Errorf("apply failed")
			}
			if status := strVal(body, "status"); status != "" {
				fmt.Printf("  Status: %s\n", status)
			}
			if serial := strVal(body, "new_serial"); serial != "" {
				fmt.Printf("  New serial: %s\n", serial)
			}
			return nil
		},
	}
}

func newSchedulePreviewCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "preview ID",
		Short: "Preview prerequisites and conflicts for a scheduled change",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			id := args[0]
			path := "/v1/scheduled-changes/" + url.PathEscape(id) + "/preview"
			resp, data, err := client.do(http.MethodPost, path, nil, nil, "")
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
			fmt.Printf("Preview for %s (%s)\n", id, strVal(body, "zone"))
			fmt.Printf("  %s\n", strVal(body, "message"))
			if passed, ok := body["all_prerequisites_passed"].(bool); ok {
				fmt.Printf("  Prerequisites passed: %v\n", passed)
			}
			if ops, ok := body["operations_count"].(float64); ok {
				fmt.Printf("  Operations: %.0f\n", ops)
			}
			if prereqs, ok := body["prerequisites"].([]any); ok && len(prereqs) > 0 {
				fmt.Println("  Prerequisites:")
				for _, p := range prereqs {
					pm, _ := p.(map[string]any)
					icon := "✓"
					if passed, _ := pm["passed"].(bool); !passed {
						icon = "✗"
					}
					fmt.Printf("    %s %s %s — %s\n", icon, strVal(pm, "prereq_type"), strVal(pm, "name"), strVal(pm, "message"))
				}
			}
			if conflicts, ok := body["conflicts"].([]any); ok && len(conflicts) > 0 {
				fmt.Println("  Conflicts:")
				for _, c := range conflicts {
					cm, _ := c.(map[string]any)
					fmt.Printf("    %s (%s) touches %s %s\n",
						strVal(cm, "other_change_name"), strVal(cm, "other_change_id"),
						strVal(cm, "name"), strVal(cm, "type"))
				}
			}
			return nil
		},
	}
}

func newScheduleRevertCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "revert ID",
		Short: "Revert an applied scheduled change",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return scheduleAction(opts, http.MethodPost, args[0], "/revert", nil, "Reverted")
		},
	}
}

func newScheduleRevertPreviewCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "revert-preview ID",
		Short: "Preview operations that would revert an applied change",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			id := args[0]
			path := "/v1/scheduled-changes/" + url.PathEscape(id) + "/revert-preview"
			resp, data, err := client.do(http.MethodGet, path, nil, nil, "")
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
			fmt.Printf("Revert preview for %s (%s)\n", id, strVal(body, "zone"))
			if w := strVal(body, "warning"); w != "" {
				fmt.Printf("  Warning: %s\n", w)
			}
			if ops, ok := body["operations"].([]any); ok {
				fmt.Printf("  %d operation(s):\n", len(ops))
				for _, op := range ops {
					om, _ := op.(map[string]any)
					fmt.Printf("    %s %s %s ttl=%s %v\n",
						strVal(om, "action"), strVal(om, "name"), strVal(om, "type"),
						strVal(om, "ttl"), om["records"])
				}
			}
			return nil
		},
	}
}

func newScheduleEventsCmd(opts *cliOptions) *cobra.Command {
	var event, actor, zone string
	cmd := &cobra.Command{
		Use:   "events [ID]",
		Short: "List audit events (optionally for one change)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(opts)
			if err != nil {
				return err
			}
			var path string
			q := url.Values{}
			if len(args) == 1 {
				path = "/v1/scheduled-changes/" + url.PathEscape(args[0]) + "/events"
			} else {
				path = "/v1/scheduled-changes/events"
				if event != "" {
					q.Set("event", event)
				}
				if actor != "" {
					q.Set("actor", actor)
				}
				if zone != "" {
					q.Set("zone", zone)
				}
			}
			resp, data, err := client.do(http.MethodGet, path, q, nil, "")
			if err != nil {
				return err
			}
			done, err := emitResponse(opts, resp, data)
			if done {
				return err
			}
			var events []map[string]any
			var wrapped struct {
				Events []map[string]any `json:"events"`
				Total  int              `json:"total"`
			}
			if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Events != nil {
				events = wrapped.Events
			} else if err := json.Unmarshal(data, &events); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}
			if len(events) == 0 {
				fmt.Println("No events found")
				return nil
			}
			fmt.Printf("Found %d event(s):\n\n", len(events))
			fmt.Printf("  %-28s  %-16s  %-36s  %s\n", "TS", "EVENT", "CHANGE_ID", "ZONE")
			for _, e := range events {
				fmt.Printf("  %-28s  %-16s  %-36s  %s\n",
					truncate(strVal(e, "ts"), 28),
					strVal(e, "event"),
					truncate(strVal(e, "change_id"), 36),
					strVal(e, "zone"),
				)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&event, "event", "", "Filter by event type (global list only)")
	cmd.Flags().StringVar(&actor, "actor", "", "Filter by actor (global list only)")
	cmd.Flags().StringVar(&zone, "zone", "", "Filter by zone (global list only)")
	return cmd
}

func scheduleAction(opts *cliOptions, method, id, suffix string, body any, verb string) error {
	client, err := newAPIClient(opts)
	if err != nil {
		return err
	}
	path := "/v1/scheduled-changes/" + url.PathEscape(id) + suffix
	resp, data, err := client.do(method, path, nil, body, "")
	if err != nil {
		return err
	}
	done, err := emitResponse(opts, resp, data)
	if done {
		return err
	}
	var ch map[string]any
	if err := json.Unmarshal(data, &ch); err != nil {
		success(fmt.Sprintf("%s scheduled change %s", verb, id))
		return nil //nolint:nilerr
	}
	success(fmt.Sprintf("%s scheduled change %s", verb, id))
	if status := strVal(ch, "status"); status != "" {
		fmt.Printf("  Status: %s\n", status)
	}
	if msg := strVal(ch, "message"); msg != "" {
		fmt.Printf("  %s\n", msg)
	}
	return nil
}

func printChangeDetail(ch map[string]any) {
	fmt.Printf("  ID:     %s\n", strVal(ch, "id"))
	fmt.Printf("  Name:   %s\n", strVal(ch, "name"))
	fmt.Printf("  Zone:   %s\n", strVal(ch, "zone"))
	fmt.Printf("  Status: %s\n", strVal(ch, "status"))
	if s := strVal(ch, "source"); s != "" {
		fmt.Printf("  Source: %s\n", s)
	}
	if s := strVal(ch, "scheduled_at"); s != "" {
		fmt.Printf("  At:     %s\n", s)
	}
	if s := strVal(ch, "not_valid_after"); s != "" {
		fmt.Printf("  Until:  %s\n", s)
	}
	if ops, ok := ch["operations"].([]any); ok {
		fmt.Printf("  Operations (%d):\n", len(ops))
		for _, op := range ops {
			om, _ := op.(map[string]any)
			fmt.Printf("    %s %s %s ttl=%s %v\n",
				strVal(om, "action"), strVal(om, "name"), strVal(om, "type"),
				strVal(om, "ttl"), om["records"])
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}
