package migrations

import "embed"

// FS contains the forward-only migrations compiled into both deployables.
//
//go:embed *.up.sql
var FS embed.FS
