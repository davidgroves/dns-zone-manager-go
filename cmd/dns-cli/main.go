// Command dns-cli talks to the DNS Zone Manager HTTP API.
package main

import (
	"fmt"
	"os"

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
	root := newRoot(opts)
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "✗ %s\n", err.Error())
		os.Exit(1)
	}
}

func newRoot(opts *cliOptions) *cobra.Command {
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

	urlDefault := opts.baseURL
	if urlDefault == "" {
		urlDefault = envOr("DNS_API_URL", "http://localhost:8000")
	}
	keyDefault := opts.apiKey
	if keyDefault == "" {
		keyDefault = os.Getenv("DNS_API_KEY")
	}
	root.PersistentFlags().StringVar(&opts.baseURL, "url", urlDefault, "DNS API server URL")
	root.PersistentFlags().StringVar(&opts.apiKey, "api-key", keyDefault, "API key for authentication")
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
		newScheduleCmd(opts),
		newSearchCmd(opts),
		newCatalogCmd(opts),
		newRefreshCmd(opts),
		newCacheInvalidateCmd(opts),
		newAtomicCmd(opts),
		newHistoryCmd(opts),
		newRollbackCmd(opts),
		newReverseCmd(opts),
	)
	return root
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
