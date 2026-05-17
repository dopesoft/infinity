// Package db embeds Infinity's SQL migrations into the Go binary so
// `infinity migrate` works without bundling extra files at deploy time.
package db

import "embed"

//go:embed migrations/*.sql
var Migrations embed.FS

// SynonymsFile contains the FTS synonym dictionary text. Managed Postgres
// (Supabase, Neon) typically can't load synonym files from disk - see
// 003_search.sql for the graceful fallback that activates in that case.
//
//go:embed synonyms.syn
var SynonymsFile []byte
