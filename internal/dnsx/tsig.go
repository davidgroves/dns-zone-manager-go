package dnsx

import (
	"strings"

	"github.com/miekg/dns"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
)

// BuildTSIGMap builds a miekg/dns TsigSecret map (key FQDN → secret) from config entries.
func BuildTSIGMap(keys []config.TSIGKeyEntry) map[string]string {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if k.Name == "" || k.Secret.IsZero() {
			continue
		}
		name := dns.Fqdn(k.Name)
		out[name] = k.Secret.String()
	}
	return out
}

// AlgorithmFromString maps config algorithm names to miekg/dns HMAC constants.
// Unknown algorithms default to HmacSHA256.
func AlgorithmFromString(alg string) string {
	switch strings.ToLower(strings.TrimSpace(alg)) {
	case "hmac-sha256", "hmac_sha256":
		return dns.HmacSHA256
	case "hmac-sha384", "hmac_sha384":
		return dns.HmacSHA384
	case "hmac-sha512", "hmac_sha512":
		return dns.HmacSHA512
	case "hmac-sha1", "hmac_sha1":
		return dns.HmacSHA1
	case "hmac-md5", "hmac_md5", "hmac-md5.sig-alg.reg.int.":
		return dns.HmacMD5
	default:
		return dns.HmacSHA256
	}
}
