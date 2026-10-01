// Command dns-perf runs the performance scenarios and writes a PDF with each result.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/davidgroves/dns-zone-manager-go/internal/perfharness"
)

func main() {
	cfg := perfharness.ConfigFromEnv()
	root := &cobra.Command{
		Use:           "dns-perf",
		Short:         "DNS Zone Manager performance harness",
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `Run performance scenarios against the DNS Zone Manager and BIND.

Each run writes JSON, Markdown, and a PDF under perf/results, plus latest.pdf.
Flags that set the API and BIND address belong before the subcommand:

  dns-perf --bind-host 127.0.0.1 run writes-one-zone`,
	}
	root.PersistentFlags().StringVar(&cfg.APIBase, "api-base", cfg.APIBase, "API base URL")
	root.PersistentFlags().StringVar(&cfg.APIKey, "api-key", cfg.APIKey, "API key (default: $API_KEY)")
	root.PersistentFlags().StringVar(&cfg.BindHost, "bind-host", cfg.BindHost, "BIND host")
	root.PersistentFlags().IntVar(&cfg.BindPort, "bind-port", cfg.BindPort, "BIND port")

	root.AddCommand(zoneCmd(&cfg), runCmd(&cfg), reportCmd(), listCmd())

	if err := root.Execute(); err != nil {
		if !errors.Is(err, errRunFailed) {
			fmt.Fprintln(os.Stderr, "ERROR:", err)
		}
		os.Exit(1)
	}
}

var errRunFailed = errors.New("scenario reported errors")

func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available scenarios",
		RunE: func(cmd *cobra.Command, args []string) error {
			names, err := perfharness.ListScenarios()
			if err != nil {
				return err
			}
			for _, name := range names {
				fmt.Println(name)
			}
			return nil
		},
	}
}

func runCmd(cfg *perfharness.Config) *cobra.Command {
	var records int
	var preset string
	cmd := &cobra.Command{
		Use:   "run <scenario|all|path.yaml>",
		Short: "Run a named scenario",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			names := []string{args[0]}
			if args[0] == "all" {
				var err error
				names, err = perfharness.ListScenarios()
				if err != nil {
					return err
				}
			}
			failed := false
			for _, name := range names {
				sc, err := perfharness.LoadScenario(name)
				if err != nil {
					fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
					failed = true
					continue
				}
				fmt.Fprintf(os.Stderr, "==> Running scenario: %s\n", sc.Name)
				rep := perfharness.RunScenario(*cfg, sc, records, preset)
				written, err := perfharness.WriteReport(rep)
				if err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "Wrote %s\nWrote %s\nWrote %s\n", written.JSON, written.MD, written.PDF)
				fmt.Print(mustMarkdown(written.JSON))
				if len(rep.Errors) > 0 {
					failed = true
				}
			}
			if failed {
				return errRunFailed
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&records, "records", 0, "Override provision size")
	cmd.Flags().StringVar(&preset, "preset", "", "Override provision preset (100k, 1m, 5m)")
	return cmd
}

func reportCmd() *cobra.Command {
	var compareFirst string
	var pdfPath string
	cmd := &cobra.Command{
		Use:   "report [path.json]",
		Short: "Render or compare saved reports",
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("compare") {
				if len(args) != 1 {
					return fmt.Errorf("--compare needs two report JSON paths")
				}
				return nil
			}
			return cobra.MaximumNArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("compare") {
				a, err := perfharness.LoadReport(compareFirst)
				if err != nil {
					return err
				}
				b, err := perfharness.LoadReport(args[0])
				if err != nil {
					return err
				}
				fmt.Print(perfharness.CompareReports(a, b))
				return nil
			}
			path := filepath.Join(perfharness.ResultsDir(), "latest.json")
			if len(args) == 1 {
				path = args[0]
			}
			data, err := perfharness.LoadReport(path)
			if err != nil {
				return fmt.Errorf("report not found: %s", path)
			}
			fmt.Print(perfharness.RenderMarkdown(data))
			out := pdfPath
			if out == "" {
				out = pdfBeside(path)
			}
			if err := perfharness.WritePDFFile(out, data); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Wrote %s\n", out)
			return nil
		},
	}
	cmd.Flags().StringVar(&compareFirst, "compare", "", "Compare this JSON report with the next argument")
	cmd.Flags().StringVar(&pdfPath, "pdf", "", "Write the PDF to this path (default: next to the JSON)")
	return cmd
}

func zoneCmd(cfg *perfharness.Config) *cobra.Command {
	zone := &cobra.Command{Use: "zone", Short: "Generate, provision, or destroy a zone"}
	var (
		name             string
		records          int
		preset           string
		seed             int
		output           string
		skipGenerate     bool
		skipCacheRefresh bool
		deleteFile       bool
	)
	addSize := func(c *cobra.Command) {
		c.Flags().StringVar(&name, "zone", perfharness.DefaultZone, "Zone name")
		c.Flags().IntVar(&records, "records", 0, "Record count")
		c.Flags().StringVar(&preset, "preset", "", "Named size: 100k, 1m, or 5m")
		c.Flags().IntVar(&seed, "seed", 42, "Generator seed")
	}
	create := &cobra.Command{
		Use:   "create",
		Short: "Generate a zone file, load it into BIND, and refresh the API cache",
		RunE: func(cmd *cobra.Command, args []string) error {
			count, err := perfharness.ResolveRecords(preset, records)
			if err != nil {
				return err
			}
			res := perfharness.CreateZone(*cfg, name, count, seed, skipGenerate, skipCacheRefresh)
			return printProvision(res)
		},
	}
	addSize(create)
	create.Flags().BoolVar(&skipGenerate, "skip-generate", false, "Use the existing zone file")
	create.Flags().BoolVar(&skipCacheRefresh, "skip-cache-refresh", false, "Do not AXFR the zone into the API")
	generate := &cobra.Command{
		Use:   "generate",
		Short: "Write a zone file without loading it",
		RunE: func(cmd *cobra.Command, args []string) error {
			count, err := perfharness.ResolveRecords(preset, records)
			if err != nil {
				return err
			}
			gen, err := perfharness.GenerateZone(name, count, seed, output)
			if err != nil {
				return err
			}
			fmt.Printf("Wrote %d records → %s (%d bytes, %.2fs)\n", gen.Records, gen.Path, gen.Bytes, gen.Elapsed.Seconds())
			return nil
		},
	}
	addSize(generate)
	generate.Flags().StringVar(&output, "output", "", "Zone file path")
	destroy := &cobra.Command{
		Use:   "destroy",
		Short: "Remove a zone from BIND",
		RunE: func(cmd *cobra.Command, args []string) error {
			res := perfharness.DestroyZone(*cfg, name, deleteFile)
			return printProvision(res)
		},
	}
	destroy.Flags().StringVar(&name, "zone", perfharness.DefaultZone, "Zone name")
	destroy.Flags().BoolVar(&deleteFile, "delete-file", false, "Also delete the zone file")
	zone.AddCommand(create, generate, destroy)
	return zone
}

func printProvision(res perfharness.ProvisionResult) error {
	raw, err := yaml.Marshal(res)
	if err != nil {
		return err
	}
	fmt.Print(string(raw))
	if len(res.Errors) > 0 {
		return errRunFailed
	}
	return nil
}

func mustMarkdown(jsonPath string) string {
	data, err := perfharness.LoadReport(jsonPath)
	if err != nil {
		return ""
	}
	return perfharness.RenderMarkdown(data)
}

func pdfBeside(jsonPath string) string {
	ext := filepath.Ext(jsonPath)
	if ext == "" {
		return jsonPath + ".pdf"
	}
	return jsonPath[:len(jsonPath)-len(ext)] + ".pdf"
}
