# PocketBase Storage Backend Design

## Overview

Replace Charm server's SQLite and LocalFileStore backends with embedded PocketBase. This provides an admin dashboard, REST API, S3 backup capability, and realtime subscriptions.

## Port Layout

| Port  | Service |
|-------|---------|
| 35353 | SSH |
| 35354 | HTTP API |
| 35355 | Stats/Prometheus |
| 35356 | Health |
| 35357 | PocketBase Admin (new) |

## PocketBase Collections

### Built-in `users` Collection (Extended)

| Field | Type | Notes |
|-------|------|-------|
| `charm_id` | text, unique | UUID for Charm identity |
| `name` | text, unique nullable | Display name |
| `bio` | text | User bio |

### Custom Collections

| Collection | Fields | Notes |
|------------|--------|-------|
| `public_keys` | `user` (relation→users), `public_key` (text) | SSH keys, unique on user+key |
| `encrypt_keys` | `public_key` (relation→public_keys), `global_id` (text), `encrypted_key` (text) | Device encryption keys |
| `named_seqs` | `user` (relation→users), `name` (text), `seq` (number) | User-scoped counters |
| `news` | `subject` (text), `body` (text), `tags` (json) | Server announcements |
| `tokens` | `pin` (text, unique) | Temporary link tokens |
| `charm_files` | `user` (relation→users), `path` (text), `file` (file), `mode` (number) | User files, unique on user+path |

## Package Structure

```
server/
├── db/
│   ├── db.go              (interface - unchanged)
│   ├── sqlite/            (removed)
│   └── pocketbase/        (new)
│       └── db.go          (implements db.DB)
├── storage/
│   ├── storage.go         (interface - unchanged)
│   ├── local/             (removed)
│   └── pocketbase/        (new)
│       └── storage.go     (implements storage.FileStore)
└── pocketbase/            (new)
    └── pocketbase.go      (PocketBase app lifecycle)
```

## Startup Flow

1. `charm serve` starts
2. Initialize embedded PocketBase app with data dir
3. Auto-create/migrate collections on first run
4. Start PocketBase on port 35357
5. Pass PocketBase app to DB and FileStore implementations
6. Start SSH/HTTP servers using PocketBase-backed storage

## Interface Mappings

### db.DB Interface

All methods query PocketBase collections via the Go SDK instead of raw SQL.

### storage.FileStore Interface

| Method | PocketBase Operation |
|--------|---------------------|
| `Stat(charmID, path)` | Query `charm_files` by user+path, return metadata |
| `Get(charmID, path)` | Query record, return file via filesystem API |
| `Put(charmID, path, r, mode)` | Upsert record with file upload |
| `Delete(charmID, path)` | Delete record (auto-deletes file) |

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CHARM_SERVER_PB_PORT` | `35357` | PocketBase admin port |
| `CHARM_SERVER_PB_ADMIN_EMAIL` | (none) | Initial admin email |
| `CHARM_SERVER_PB_ADMIN_PASSWORD` | (none) | Initial admin password |

### Data Directory

```
{CHARM_SERVER_DATA_DIR}/
├── pb_data/
│   ├── data.db        (PocketBase database)
│   └── storage/       (uploaded files)
└── pb_migrations/     (optional)
```

### S3 Backup

Configured via PocketBase admin UI at `/_/settings/storage`. No Charm-specific configuration needed.

## Dependencies

### Added

- `github.com/pocketbase/pocketbase`

### Removed

- `modernc.org/sqlite`

## Dockerfile Changes

Add exposed port:

```dockerfile
EXPOSE 35357/tcp
```
