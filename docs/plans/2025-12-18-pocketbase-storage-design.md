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

All collections use `core.NewBaseCollection()` - we do not use PocketBase's auth collection since Charm users are created automatically on SSH key connection without credentials.

### Collections

| Collection | Fields | Notes |
|------------|--------|-------|
| `charm_users` | `charm_id` (text, unique), `name` (text, unique nullable), `email` (text), `bio` (text) | Charm user accounts |
| `public_keys` | `user` (relation→charm_users), `public_key` (text) | SSH keys, unique on user+key |
| `encrypt_keys` | `public_key` (relation→public_keys), `global_id` (text), `encrypted_key` (text) | Device encryption keys |
| `named_seqs` | `user` (relation→charm_users), `name` (text), `seq` (number) | User-scoped counters |
| `news` | `subject` (text), `body` (text), `tags` (json) | Server announcements |
| `tokens` | `pin` (text, unique) | Temporary link tokens |
| `charm_files` | `charm_id` (text), `path` (text), `file` (file, optional), `is_dir` (bool), `mode` (number) | User files, unique on charm_id+path |

### Directory Support

Directories are stored as records with `is_dir: true` and no file attachment. The `Get` method returns JSON directory listings for directory records, matching current LocalFileStore behavior.

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

All methods query PocketBase collections via the Go SDK instead of raw SQL. PocketBase's `app.RunInTransaction()` replaces the custom SQLite transaction wrapper.

### storage.FileStore Interface

| Method | PocketBase Operation |
|--------|---------------------|
| `Stat(charmID, path)` | Query `charm_files` by charm_id+path, return metadata |
| `Get(charmID, path)` | Query record, return file via filesystem API |
| `Put(charmID, path, r, mode)` | Upsert record with file upload |
| `Delete(charmID, path)` | Delete record (auto-deletes file) |

### fs.File Wrapper

The `Get` method must return `fs.File`. Create wrapper types:

- `PocketbaseFile` - wraps PocketBase file response, implements `Read`, `Close`, `Stat`
- Reuse existing `charmfs.DirFile` for directory listings (returns JSON)

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
