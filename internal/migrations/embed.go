// Package migrations embeds the SQL schema files into the binary so the server
// is fully self-contained — no schema directory is required on the deployment
// host. The embedded FS is consumed by goose.Up at startup in cmd/api/main.go.
package migrations

import "embed"

//go:embed schema
var MigrationFiles embed.FS
