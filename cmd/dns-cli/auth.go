package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newAuthCmd(opts *cliOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate to the DNS Zone Manager API (gh-style)",
	}
	cmd.AddCommand(
		newAuthLoginCmd(opts),
		newAuthLogoutCmd(opts),
		newAuthStatusCmd(opts),
		newAuthTokenCmd(opts),
	)
	return cmd
}

func newAuthLoginCmd(opts *cliOptions) *cobra.Command {
	var (
		issuer   string
		clientID string
		scope    string
		withTok  bool
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in via OAuth device code (or --with-token)",
		Long: `Authenticate and store a time-limited access token for the API host.

Default flow (RFC 8628 device code):
  1. dns-cli requests a device code from the OIDC issuer
  2. You open the verification URL and approve the sign-in
  3. The access + refresh tokens are stored in ~/.config/dns-cli/hosts.yml

Devcontainer defaults (DNS_CLI_ISSUER / DNS_CLI_CLIENT_ID / DNS_CLI_SCOPE)
point at the local Entra emulator. Against real Entra ID:

  dns-cli auth login \
    --url https://dns.example.com \
    --issuer https://login.microsoftonline.com/<tenant>/v2.0 \
    --client-id <public-app-id> \
    --scope 'api://<api-app>/access_as_user offline_access'

--with-token reads a bearer token from stdin (no refresh).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if issuer == "" {
				issuer = os.Getenv("DNS_CLI_ISSUER")
			}
			if clientID == "" {
				clientID = os.Getenv("DNS_CLI_CLIENT_ID")
			}
			if scope == "" {
				scope = envOr("DNS_CLI_SCOPE", "api://dns-zone-manager/access_as_user openid profile offline_access")
			}

			if withTok {
				return authLoginWithToken(opts)
			}
			if issuer == "" || clientID == "" {
				return fmt.Errorf("device-code login requires --issuer and --client-id (or DNS_CLI_ISSUER / DNS_CLI_CLIENT_ID)")
			}
			return authLoginDevice(opts, issuer, clientID, scope)
		},
	}
	cmd.Flags().StringVar(&issuer, "issuer", "", "OIDC issuer URL (default: DNS_CLI_ISSUER)")
	cmd.Flags().StringVar(&clientID, "client-id", "", "OAuth public client id (default: DNS_CLI_CLIENT_ID)")
	cmd.Flags().StringVar(&scope, "scope", "", "OAuth scopes (default: DNS_CLI_SCOPE)")
	cmd.Flags().BoolVar(&withTok, "with-token", false, "Read a bearer token from stdin instead of device code")
	return cmd
}

func authLoginWithToken(opts *cliOptions) error {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return fmt.Errorf("empty token on stdin")
	}
	user, exp := decodeTokenDisplay(token)
	c := hostCredentials{
		Token:     token,
		User:      user,
		ExpiresAt: exp,
	}
	if err := storeHost(opts.baseURL, c); err != nil {
		return err
	}
	key, _ := hostKey(opts.baseURL)
	success(fmt.Sprintf("Logged in to %s as %s (token from stdin)", key, displayUser(user)))
	return nil
}

func authLoginDevice(opts *cliOptions, issuer, clientID, scope string) error {
	disc, err := fetchOIDCDiscovery(issuer)
	if err != nil {
		return err
	}
	if disc.DeviceAuthURL == "" || disc.TokenURL == "" {
		return fmt.Errorf("issuer %s does not advertise device_authorization_endpoint / token_endpoint", issuer)
	}

	dc, err := requestDeviceCode(disc.DeviceAuthURL, clientID, scope)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "! First copy your one-time code: %s\n", dc.UserCode)
	fmt.Fprintf(os.Stderr, "- Open %s in your browser and paste the code when prompted\n", dc.VerificationURI)
	if dc.Message != "" {
		fmt.Fprintf(os.Stderr, "  %s\n", dc.Message)
	}

	tok, err := pollDeviceToken(disc.TokenURL, clientID, dc)
	if err != nil {
		return err
	}
	user, _ := decodeTokenDisplay(tok.AccessToken)
	if user == "" {
		user, _ = decodeTokenDisplay(tok.IDToken)
	}
	exp := time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	c := hostCredentials{
		Token:        tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		User:         user,
		ExpiresAt:    exp,
		Issuer:       issuer,
		ClientID:     clientID,
		Scope:        scope,
		TokenURL:     disc.TokenURL,
	}
	if err := storeHost(opts.baseURL, c); err != nil {
		return err
	}
	key, _ := hostKey(opts.baseURL)
	success(fmt.Sprintf("Logged in to %s as %s (token expires in %s)",
		key, displayUser(user), formatTTL(time.Until(exp))))
	return nil
}

func newAuthLogoutCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials for the API host",
		RunE: func(cmd *cobra.Command, _ []string) error {
			key, err := hostKey(opts.baseURL)
			if err != nil {
				return err
			}
			if err := deleteHost(opts.baseURL); err != nil {
				return err
			}
			success(fmt.Sprintf("Logged out of %s", key))
			return nil
		},
	}
}

func newAuthStatusCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show login status for the API host",
		RunE: func(cmd *cobra.Command, _ []string) error {
			key, err := hostKey(opts.baseURL)
			if err != nil {
				return err
			}
			c, ok, err := lookupHost(opts.baseURL)
			if err != nil {
				return err
			}
			if !ok || c.Token == "" {
				fmt.Printf("Not logged in to %s\n", key)
				return nil
			}
			fmt.Printf("Logged in to %s\n", key)
			fmt.Printf("  User:    %s\n", displayUser(c.User))
			if !c.ExpiresAt.IsZero() {
				ttl := time.Until(c.ExpiresAt)
				if ttl > 0 {
					fmt.Printf("  Expires: %s (%s)\n", c.ExpiresAt.Format(time.RFC3339), formatTTL(ttl))
				} else {
					fmt.Printf("  Expires: %s (expired)\n", c.ExpiresAt.Format(time.RFC3339))
				}
			}
			if c.Issuer != "" {
				fmt.Printf("  Issuer:  %s\n", c.Issuer)
			}
			// Live check against the API when possible.
			client, err := newAPIClient(opts)
			if err != nil {
				fmt.Printf("  Live:    (skipped: %v)\n", err)
				return nil
			}
			resp, data, err := client.do(http.MethodGet, "/v1/auth/validate", nil, nil, "")
			if err != nil {
				fmt.Printf("  Live:    error: %v\n", err)
				return nil
			}
			if resp.StatusCode >= 300 {
				fmt.Printf("  Live:    %s\n", apiError(resp.StatusCode, data))
				return nil
			}
			fmt.Printf("  Live:    ok\n")
			return nil
		},
	}
}

func newAuthTokenCmd(opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "token",
		Short: "Print the stored access token (refreshing if needed)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			token, err := resolveBearerToken(opts, true)
			if err != nil {
				return err
			}
			fmt.Println(token)
			return nil
		},
	}
}

type oidcDiscovery struct {
	Issuer        string `json:"issuer"`
	TokenURL      string `json:"token_endpoint"`
	DeviceAuthURL string `json:"device_authorization_endpoint"`
}

type deviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
	Message                 string `json:"message"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

func fetchOIDCDiscovery(issuer string) (oidcDiscovery, error) {
	u := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	resp, err := http.Get(u) //nolint:gosec // issuer is user-configured
	if err != nil {
		return oidcDiscovery{}, fmt.Errorf("OIDC discovery: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return oidcDiscovery{}, fmt.Errorf("OIDC discovery %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var d oidcDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return oidcDiscovery{}, fmt.Errorf("OIDC discovery decode: %w", err)
	}
	return d, nil
}

func requestDeviceCode(endpoint, clientID, scope string) (deviceCodeResponse, error) {
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("scope", scope)
	resp, err := http.PostForm(endpoint, form) //nolint:gosec
	if err != nil {
		return deviceCodeResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return deviceCodeResponse{}, fmt.Errorf("device code request failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var dc deviceCodeResponse
	if err := json.Unmarshal(body, &dc); err != nil {
		return deviceCodeResponse{}, err
	}
	if dc.Interval <= 0 {
		dc.Interval = 5
	}
	return dc, nil
}

func pollDeviceToken(tokenURL, clientID string, dc deviceCodeResponse) (tokenResponse, error) {
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	if dc.ExpiresIn <= 0 {
		deadline = time.Now().Add(15 * time.Minute)
	}
	interval := time.Duration(dc.Interval) * time.Second
	client := &http.Client{Timeout: 30 * time.Second}
	for {
		if time.Now().After(deadline) {
			return tokenResponse{}, fmt.Errorf("device code expired; run auth login again")
		}
		form := url.Values{}
		form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
		form.Set("client_id", clientID)
		form.Set("device_code", dc.DeviceCode)
		resp, err := client.PostForm(tokenURL, form) //nolint:gosec
		if err != nil {
			return tokenResponse{}, err
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		var tok tokenResponse
		_ = json.Unmarshal(body, &tok)
		if tok.AccessToken != "" {
			return tok, nil
		}
		switch tok.Error {
		case "authorization_pending":
			time.Sleep(interval)
			continue
		case "slow_down":
			interval += 5 * time.Second
			time.Sleep(interval)
			continue
		case "authorization_declined":
			return tokenResponse{}, fmt.Errorf("authorization declined")
		case "expired_token", "bad_verification_code":
			return tokenResponse{}, fmt.Errorf("device code %s; run auth login again", tok.Error)
		default:
			if tok.Error != "" {
				return tokenResponse{}, fmt.Errorf("%s: %s", tok.Error, tok.ErrorDesc)
			}
			return tokenResponse{}, fmt.Errorf("token endpoint (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}
}

func refreshAccessToken(c hostCredentials) (hostCredentials, error) {
	if c.RefreshToken == "" || c.TokenURL == "" || c.ClientID == "" {
		return c, fmt.Errorf("stored token expired; run dns-cli auth login")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", c.ClientID)
	form.Set("refresh_token", c.RefreshToken)
	if c.Scope != "" {
		form.Set("scope", c.Scope)
	}
	resp, err := http.PostForm(c.TokenURL, form) //nolint:gosec
	if err != nil {
		return c, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var tok tokenResponse
	_ = json.Unmarshal(body, &tok)
	if tok.AccessToken == "" {
		return c, fmt.Errorf("refresh failed: %s", strings.TrimSpace(string(body)))
	}
	c.Token = tok.AccessToken
	if tok.RefreshToken != "" {
		c.RefreshToken = tok.RefreshToken
	}
	if tok.ExpiresIn > 0 {
		c.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	user, _ := decodeTokenDisplay(tok.AccessToken)
	if user != "" {
		c.User = user
	}
	return c, nil
}

func resolveBearerToken(opts *cliOptions, allowRefresh bool) (string, error) {
	if t := os.Getenv("DNS_API_TOKEN"); t != "" {
		return t, nil
	}
	c, ok, err := lookupHost(opts.baseURL)
	if err != nil {
		return "", err
	}
	if !ok || c.Token == "" {
		return "", fmt.Errorf("not logged in: run `dns-cli auth login` or set DNS_API_KEY / DNS_API_TOKEN")
	}
	if allowRefresh && !c.ExpiresAt.IsZero() && time.Until(c.ExpiresAt) < 60*time.Second {
		refreshed, err := refreshAccessToken(c)
		if err != nil {
			return "", err
		}
		if err := storeHost(opts.baseURL, refreshed); err != nil {
			return "", err
		}
		return refreshed.Token, nil
	}
	return c.Token, nil
}

func decodeTokenDisplay(token string) (user string, exp time.Time) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Some encoders pad; try StdEncoding with padding.
		padded := parts[1] + strings.Repeat("=", (4-len(parts[1])%4)%4)
		payload, err = base64.URLEncoding.DecodeString(padded)
		if err != nil {
			return "", time.Time{}
		}
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", time.Time{}
	}
	for _, k := range []string{"preferred_username", "upn", "email", "name", "sub"} {
		if v, ok := claims[k].(string); ok && v != "" {
			user = v
			break
		}
	}
	if n, ok := claims["exp"].(float64); ok {
		exp = time.Unix(int64(n), 0)
	}
	return user, exp
}

func displayUser(user string) string {
	if user == "" {
		return "(unknown)"
	}
	return user
}

func formatTTL(d time.Duration) string {
	if d < 0 {
		return "expired"
	}
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm", m)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
