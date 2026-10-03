// Command dns-perf runs the performance scenarios and writes a PDF with each result.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	var rps []float64
	var lowestRPS, highestRPS, stepRPS, stepAlias float64
	var duration float64
	var pdfPath string
	var mdPath string
	cmd := &cobra.Command{
		Use:   "run <scenario|all|path.yaml>",
		Short: "Run a named scenario",
		Long: `Run a named scenario from perf/scenarios.

Override offered write rate with --rps, or sweep with
--lowest-rps / --highest-rps / --step-rps. Use --duration for each step.

  ./perf.sh run writes-one-zone --lowest-rps 100 --highest-rps 1000 --step-rps 100 --duration 30
  ./perf.sh run writes-many-zones --lowest-rps 100 --highest-rps 1000 --step-rps 100 --duration 30
  ./perf.sh report --compare one.json many.json --pdf zone-compare.pdf`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			names := []string{args[0]}
			if args[0] == "all" {
				var err error
				names, err = perfharness.ListScenarios()
				if err != nil {
					return err
				}
			}
			step := stepRPS
			if stepAlias > 0 {
				if step > 0 && step != stepAlias {
					return fmt.Errorf("--step-rps (%g) and --step (%g) disagree", step, stepAlias)
				}
				step = stepAlias
			}
			ov := perfharness.RunOverrides{
				Records: records, Preset: preset, RPS: append([]float64(nil), rps...),
				LowestRPS: lowestRPS, HighestRPS: highestRPS, StepRPS: step, Duration: duration,
			}
			if err := perfharness.ResolveRateOverrides(&ov); err != nil {
				return err
			}
			multi := len(names) > 1
			failed := false
			for _, name := range names {
				sc, err := perfharness.LoadScenario(name)
				if err != nil {
					fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
					failed = true
					continue
				}
				fmt.Fprintf(os.Stderr, "==> Running scenario: %s\n", sc.Name)
				if len(ov.RPS) > 0 {
					fmt.Fprintf(os.Stderr, "    rps steps: %v", ov.RPS)
					if ov.Duration > 0 {
						fmt.Fprintf(os.Stderr, " (duration %.0fs)", ov.Duration)
					}
					fmt.Fprintln(os.Stderr)
				}
				rep := perfharness.RunScenario(*cfg, sc, ov)
				opts := perfharness.WriteOptions{
					PDFPath: pathForScenario(pdfPath, sc.Name, multi),
					MDPath:  pathForScenario(mdPath, sc.Name, multi),
				}
				written, err := perfharness.WriteReport(rep, opts)
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
	cmd.Flags().Float64SliceVar(&rps, "rps", nil, "Override target RPS; replaces scenario load steps")
	cmd.Flags().Float64Var(&lowestRPS, "lowest-rps", 0, "Start of rate sweep (inclusive)")
	cmd.Flags().Float64Var(&highestRPS, "highest-rps", 0, "End of rate sweep (inclusive)")
	cmd.Flags().Float64Var(&stepRPS, "step-rps", 0, "Increment between rates")
	cmd.Flags().Float64Var(&stepAlias, "step", 0, "Alias for --step-rps")
	cmd.Flags().Float64Var(&duration, "duration", 0, "Override step duration in seconds")
	cmd.Flags().StringVar(&pdfPath, "pdf", "", "Write the PDF report to this path")
	cmd.Flags().StringVar(&mdPath, "markdown", "", "Write the Markdown report to this path")
	return cmd
}

func reportCmd() *cobra.Command {
	var compareFirst string
	var pdfPath string
	var mdPath string
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
				md := perfharness.CompareReports(a, b)
				fmt.Print(md)
				if mdPath != "" {
					if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
						return err
					}
					fmt.Fprintf(os.Stderr, "Wrote %s\n", mdPath)
				}
				out := pdfPath
				if out == "" {
					out = filepath.Join(perfharness.ResultsDir(), "compare.pdf")
				}
				if err := perfharness.WriteComparePDF(out, a, b); err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "Wrote %s\n", out)
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
			md := perfharness.RenderMarkdown(data)
			fmt.Print(md)
			if mdPath != "" {
				if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "Wrote %s\n", mdPath)
			}
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
	cmd.Flags().StringVar(&pdfPath, "pdf", "", "Write the PDF to this path")
	cmd.Flags().StringVar(&mdPath, "markdown", "", "Write Markdown to this path")
	return cmd
}

func pathForScenario(path, scenario string, multi bool) string {
	if path == "" {
		return ""
	}
	if !multi {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	return base + "-" + scenario + ext
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
