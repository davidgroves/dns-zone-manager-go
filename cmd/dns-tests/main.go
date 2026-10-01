// Command dns-tests runs the same suites as tests.sh and can write a PDF report.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	var (
		all         bool
		integration bool
		reportPath  string
	)
	root := &cobra.Command{
		Use:           "dns-tests",
		Short:         "Run DNS Zone Manager test suites",
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `Run the DNS Zone Manager tests.

By default this runs Go unit tests and the frontend vitest suite, matching
./tests.sh. --all adds Go integration tests and Playwright when the API and
Vite dev server are already up. --integration runs only the Go integration tests.

Pass --report path.pdf to write a one-document summary after the run. The
report is written whether the suites pass or fail.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := options{
				unit:        !integration || all,
				integration: integration || all,
				e2e:         all,
				reportPath:  reportPath,
			}
			// --integration alone skips unit and Playwright, matching tests.sh.
			if integration && !all {
				opts.unit = false
				opts.e2e = false
			}
			return execute(opts)
		},
	}
	root.Flags().BoolVar(&all, "all", false, "Unit tests plus integration and Playwright")
	root.Flags().BoolVar(&integration, "integration", false, "Run only Go integration tests")
	root.Flags().StringVar(&reportPath, "report", "", "Write a PDF report to this path")

	if err := root.Execute(); err != nil {
		if !errors.Is(err, errTestsFailed) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}
