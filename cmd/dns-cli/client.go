package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type apiClient struct {
	opts    *cliOptions
	baseURL *url.URL
	client  *http.Client
}

func newAPIClient(opts *cliOptions) (*apiClient, error) {
	base := strings.TrimRight(opts.baseURL, "/")
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("invalid --url: %w", err)
	}
	// Resolve credentials early so callers get a clear error before the first request.
	if strings.TrimSpace(opts.apiKey) == "" && strings.TrimSpace(opts.token) == "" {
		if _, err := resolveBearerToken(opts, false); err != nil {
			return nil, err
		}
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
	if key := strings.TrimSpace(c.opts.apiKey); key != "" {
		req.Header.Set("X-API-Key", key)
	}
	bearer := strings.TrimSpace(c.opts.token)
	if bearer == "" {
		if t, err := resolveBearerToken(c.opts, true); err == nil {
			bearer = t
		} else if key := strings.TrimSpace(c.opts.apiKey); key == "" {
			return nil, nil, err
		}
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
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

// emitResponse prints JSON when requested, otherwise returns whether the
// caller should continue with human formatting. Errors on non-2xx.
func emitResponse(opts *cliOptions, resp *http.Response, data []byte) (done bool, err error) {
	if opts.outputJSON {
		printJSON(data)
		if resp.StatusCode >= 300 {
			return true, apiError(resp.StatusCode, data)
		}
		return true, nil
	}
	if resp.StatusCode >= 300 {
		return true, apiError(resp.StatusCode, data)
	}
	return false, nil
}

func handleMutation(opts *cliOptions, resp *http.Response, data []byte, successMsg string) error {
	done, err := emitResponse(opts, resp, data)
	if done {
		return err
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

func readFileOrStdin(file string, prompt string) (string, error) {
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	stat, _ := os.Stdin.Stat()
	if (stat.Mode()&os.ModeCharDevice) != 0 && prompt != "" {
		fmt.Fprintln(os.Stderr, prompt)
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func loadJSONFile(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(b, &payload); err != nil {
		return nil, fmt.Errorf("invalid JSON in %s: %w", path, err)
	}
	return payload, nil
}

func strVal(m map[string]any, key string) string {
	if v, ok := m[key]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}
