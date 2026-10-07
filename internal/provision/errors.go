package provision

import "errors"

var (
	ErrDisabled        = errors.New("rndc is not configured")
	ErrCatalogDisabled = errors.New("catalog is not configured")
	ErrZoneExists      = errors.New("zone already exists")
	ErrZoneNotFound    = errors.New("zone not found")
	ErrNotReady        = errors.New("zone did not become ready")
)
