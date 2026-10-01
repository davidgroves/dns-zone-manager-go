package config

import (
	"reflect"
	"strconv"
	"strings"
	"time"

	mapstructure "github.com/go-viper/mapstructure/v2"
)

func mapstructureDecodeHook() mapstructure.DecodeHookFunc {
	return mapstructure.ComposeDecodeHookFunc(
		durationDecodeHook(),
		floatDurationDecodeHook(),
		secretDecodeHook(),
	)
}

func secretDecodeHook() mapstructure.DecodeHookFunc {
	return func(from reflect.Type, to reflect.Type, data any) (any, error) {
		if to != reflect.TypeOf(Secret{}) {
			return data, nil
		}
		switch v := data.(type) {
		case string:
			var s Secret
			s.Set(v)
			return s, nil
		case nil:
			return Secret{}, nil
		default:
			return data, nil
		}
	}
}

func durationDecodeHook() mapstructure.DecodeHookFunc {
	return func(from reflect.Type, to reflect.Type, data any) (any, error) {
		if to != reflect.TypeOf(time.Duration(0)) {
			return data, nil
		}
		switch v := data.(type) {
		case string:
			if v == "" {
				return time.Duration(0), nil
			}
			if d, err := time.ParseDuration(v); err == nil {
				return d, nil
			}
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return time.Duration(f * float64(time.Second)), nil
			}
			return data, nil
		case float64:
			return time.Duration(v * float64(time.Second)), nil
		case int:
			return time.Duration(v) * time.Second, nil
		case int64:
			return time.Duration(v) * time.Second, nil
		default:
			return data, nil
		}
	}
}

func floatDurationDecodeHook() mapstructure.DecodeHookFunc {
	return func(from reflect.Type, to reflect.Type, data any) (any, error) {
		if to.Kind() != reflect.Float64 {
			return data, nil
		}
		switch v := data.(type) {
		case int:
			return float64(v), nil
		case int64:
			return float64(v), nil
		case string:
			if v == "" {
				return 0.0, nil
			}
			return strconv.ParseFloat(strings.TrimSpace(v), 64)
		default:
			return data, nil
		}
	}
}

func unmarshalSettings(raw map[string]any) (*Settings, error) {
	var s Settings
	dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		TagName:          "mapstructure",
		Result:           &s,
		WeaklyTypedInput: true,
		DecodeHook:       mapstructureDecodeHook(),
	})
	if err != nil {
		return nil, err
	}
	if err := dec.Decode(raw); err != nil {
		return nil, err
	}
	return &s, nil
}
