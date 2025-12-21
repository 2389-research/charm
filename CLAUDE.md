# charm Library

This is the 2389-research fork of charmbracelet/charm with self-hosted server support and KV improvements.

## Repository Boundaries

When working in this repo, stay focused on charm library code only. If downstream clients (bbs-mcp, memo, etc.) need updates to use new features:

1. Document what changes they need (text file or release notes)
2. Do NOT make changes in other repos unless explicitly asked
3. Ask: "Want me to update the client too, or just document it?"

## Key Packages

- `kv/` - Key-value store with cloud sync (BadgerDB-based)
- `client/` - Charm Cloud client
- `fs/` - Encrypted filesystem
- `server/` - Self-hosted server implementation

## Testing

Run tests with: `go test ./...`

Note: Some packages have no tests (UI, server internals). The KV and client packages have good coverage.

## Releasing

Use the `releasing-software` skill. Last release: v0.14.0 (read-only fallback).
