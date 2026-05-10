// Package config exposes baked-in default configuration files. The binary
// ships these so the runtime container (Railway distroless) doesn't need
// the source tree at /app/ — same pattern as core/db/migrations.go and
// core/internal/soul.
package config

import _ "embed"

// MCPYAML is the canonical MCP server registry, bundled into the binary at
// build time. LoadMCPConfig falls back to this when no on-disk file is
// found at the configured path.
//
//go:embed mcp.yaml
var MCPYAML []byte
