package notifications

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
)

var defaultAuthHeaders = map[string]string{
	"hmac":   "X-DNS-Signature",
	"header": "X-API-Key",
}

// EffectiveHeader returns the header name for header/hmac auth.
func EffectiveHeader(auth config.WebhookAuth) string {
	if auth.Header != "" {
		return auth.Header
	}
	if h, ok := defaultAuthHeaders[auth.Type]; ok {
		return h
	}
	return "X-API-Key"
}

// TimestampHeader returns the header carrying the signed timestamp for hmac auth.
func TimestampHeader(auth config.WebhookAuth) string {
	if auth.TimestampHeader != "" {
		return auth.TimestampHeader
	}
	return "X-DNS-Timestamp"
}

// SigningInput builds the HMAC signing input: timestamp + "." + body.
func SigningInput(timestamp string, body []byte) []byte {
	out := append([]byte(timestamp), '.')
	return append(out, body...)
}

// ComputeSignature returns algorithm=hexdigest HMAC over SigningInput.
func ComputeSignature(secret, algorithm, timestamp string, body []byte) (string, error) {
	var h func() hash.Hash
	switch strings.ToLower(algorithm) {
	case "", "sha256":
		h = sha256.New
		algorithm = "sha256"
	case "sha512":
		h = sha512.New
	default:
		return "", fmt.Errorf("unsupported hmac algorithm %q", algorithm)
	}
	mac := hmac.New(h, []byte(secret))
	_, _ = mac.Write(SigningInput(timestamp, body))
	return algorithm + "=" + hex.EncodeToString(mac.Sum(nil)), nil
}

// BuildAuthHeaders builds authentication headers for one webhook request.
func BuildAuthHeaders(target config.WebhookTarget, body []byte, timestamp string) (map[string]string, error) {
	auth := target.Auth
	typ := auth.Type
	if typ == "" {
		typ = "none"
	}

	switch typ {
	case "none":
		return map[string]string{}, nil

	case "bearer":
		secret := auth.Secret.String()
		if secret == "" {
			secret = auth.Token.String()
		}
		return map[string]string{"Authorization": "Bearer " + secret}, nil

	case "basic":
		raw := auth.Username + ":" + auth.Password.String()
		enc := base64.StdEncoding.EncodeToString([]byte(raw))
		return map[string]string{"Authorization": "Basic " + enc}, nil

	case "header":
		secret := auth.Secret.String()
		if secret == "" {
			secret = auth.Token.String()
		}
		return map[string]string{EffectiveHeader(auth): secret}, nil

	case "hmac":
		secret := auth.Secret.String()
		if secret == "" {
			secret = auth.Token.String()
		}
		alg := auth.Algorithm
		if alg == "" {
			alg = "sha256"
		}
		sig, err := ComputeSignature(secret, alg, timestamp, body)
		if err != nil {
			return nil, err
		}
		return map[string]string{
			EffectiveHeader(auth): sig,
			TimestampHeader(auth): timestamp,
		}, nil

	default:
		return nil, fmt.Errorf("unknown webhook auth type: %s", typ)
	}
}

// TargetSecrets returns every secret string associated with a target for redaction.
func TargetSecrets(target config.WebhookTarget) []string {
	var out []string
	if u := target.URL.String(); u != "" {
		out = append(out, u)
	}
	if s := target.Auth.Secret.String(); s != "" {
		out = append(out, s)
	}
	if s := target.Auth.Token.String(); s != "" {
		out = append(out, s)
	}
	if s := target.Auth.Password.String(); s != "" {
		out = append(out, s)
	}
	return out
}

// Redact replaces any of the target's secrets appearing in message.
func Redact(message string, target config.WebhookTarget) string {
	for _, secret := range TargetSecrets(target) {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "***")
		}
	}
	return message
}
