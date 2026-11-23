// Package migration embeds the application database schema as numbered SQL
// files. Each file is NNNN_description.sql; its number becomes the
// database's user_version once applied. A shipped file is never edited —
// a schema change is a new file.
package migration

import "embed"

// Files holds every migration, applied in name order.
//
//go:embed *.sql
var Files embed.FS
