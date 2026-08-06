package migrations

import "embed"

// FS contains SQL migrations embedded into the backend binary.
//
//go:embed *.sql
var FS embed.FS
