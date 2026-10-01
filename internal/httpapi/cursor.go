package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// ZoneCursor is opaque pagination state for zone lists: {"n":"zone."}.
type ZoneCursor struct {
	N string `json:"n"`
}

// RRsetCursorPayload is opaque pagination for RRsets: {"n":"name.","t":"A"}.
type RRsetCursorPayload struct {
	N string `json:"n"`
	T string `json:"t"`
}

// NameCursorPayload is opaque pagination for name-ordered lists: {"n":"name."}.
type NameCursorPayload struct {
	N string `json:"n"`
}

// GlobalSearchCursor is opaque pagination for global search: {"z":"zone.","n":"name."}.
type GlobalSearchCursor struct {
	Z string `json:"z"`
	N string `json:"n"`
}

// EncodeCursor base64url-encodes a JSON cursor payload (no padding).
func EncodeCursor(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// DecodeCursor base64url-decodes into dest.
func DecodeCursor(s string, dest any) error {
	if s == "" {
		return fmt.Errorf("empty cursor")
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		// Accept standard base64url with padding too.
		b, err = base64.URLEncoding.DecodeString(s)
		if err != nil {
			return fmt.Errorf("invalid cursor encoding: %w", err)
		}
	}
	if err := json.Unmarshal(b, dest); err != nil {
		return fmt.Errorf("invalid cursor payload: %w", err)
	}
	return nil
}

func encodeZoneCursor(name string) string {
	s, _ := EncodeCursor(ZoneCursor{N: name})
	return s
}

func decodeZoneCursor(s string) (string, error) {
	var c ZoneCursor
	if err := DecodeCursor(s, &c); err != nil {
		return "", err
	}
	if c.N == "" {
		return "", fmt.Errorf("cursor missing n")
	}
	return c.N, nil
}

func encodeRRsetCursor(name, typ string) string {
	s, _ := EncodeCursor(RRsetCursorPayload{N: name, T: typ})
	return s
}

func decodeRRsetCursor(s string) (name, typ string, err error) {
	var c RRsetCursorPayload
	if err = DecodeCursor(s, &c); err != nil {
		return "", "", err
	}
	if c.N == "" || c.T == "" {
		return "", "", fmt.Errorf("cursor missing n/t")
	}
	return c.N, c.T, nil
}

func encodeNameCursor(name string) string {
	s, _ := EncodeCursor(NameCursorPayload{N: name})
	return s
}

func decodeNameCursor(s string) (string, error) {
	var c NameCursorPayload
	if err := DecodeCursor(s, &c); err != nil {
		return "", err
	}
	if c.N == "" {
		return "", fmt.Errorf("cursor missing n")
	}
	return c.N, nil
}

func encodeGlobalCursor(zone, name string) string {
	s, _ := EncodeCursor(GlobalSearchCursor{Z: zone, N: name})
	return s
}

func decodeGlobalCursor(s string) (zone, name string, err error) {
	var c GlobalSearchCursor
	if err = DecodeCursor(s, &c); err != nil {
		return "", "", err
	}
	if c.Z == "" || c.N == "" {
		return "", "", fmt.Errorf("cursor missing z/n")
	}
	return c.Z, c.N, nil
}
