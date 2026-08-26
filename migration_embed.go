package migrations

import "embed"

// Files is the immutable migration set shipped with every application build.
//
//go:embed migrations/*.sql
var Files embed.FS
