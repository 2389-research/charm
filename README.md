# 2389 Charm

A set of tools that makes adding a backend to your terminal-based applications fun and easy. Quickly build modern CLI applications without worrying about user accounts, data storage and encryption.

> **Attribution**: This is a fork of [Charm](https://github.com/charmbracelet/charm) by [Charmbracelet](https://charm.sh), originally released under the MIT license. We're grateful for their excellent work making terminal apps delightful.

## Features

* **Charm KV**: An embeddable, encrypted, cloud-synced key-value store built on [BadgerDB][badger]
* **Charm FS**: A Go `fs.FS` compatible cloud-based user filesystem
* **Charm Crypt**: End-to-end encryption for stored data and on-demand encryption for arbitrary data
* **Charm Accounts**: Invisible user account creation and authentication

## Server

The 2389 Charm server is hosted at `charm.2389.dev`. All clients point there by default.

## Installation

Build from source:

```bash
git clone https://github.com/2389-research/charm.git
cd charm
go build -o charm .
```

## Charm KV

A powerful, embeddable key-value store built on [BadgerDB][badger]. Store user data, configuration, create a cache or even store large files as values.

```go
import "github.com/2389-research/charm/kv"

// Open a database (or create one if it doesn't exist)
db, err := kv.OpenWithDefaults("my-db")
if err != nil {
    log.Fatal(err)
}
defer db.Close()

// Fetch updates
if err := db.Sync(); err != nil {
    log.Fatal(err)
}

// Save some data
if err := db.Set([]byte("key"), []byte("value")); err != nil {
    log.Fatal(err)
}
```

For details, see [the Charm KV docs][kv].

## Charm FS

Each user has a virtual personal filesystem on the server. Charm FS provides a Go [fs.FS](https://golang.org/pkg/io/fs/) implementation with additional write and delete methods.

```go
import charmfs "github.com/2389-research/charm/fs"

// Open the user's filesystem
cfs, err := charmfs.NewFS()
if err != nil {
    log.Fatal(err)
}

// Save a file
data := bytes.NewBuffer([]byte("some data"))
if err := cfs.WriteFile("./path/to/file", data, fs.FileMode(0644), int64(data.Len())); err != nil {
    log.Fatal(err)
}

// Read a file
content, err := cfs.ReadFile("./path/to/file")
if err != nil {
    log.Fatal(err)
}
```

For more, see [the Charm FS docs][fs].

## Charm Crypt

All data sent to the server is fully encrypted on the client. Charm Crypt provides methods for easily encrypting and decrypting data. All key management and account linking is handled seamlessly.

For more, see [the Charm Crypt docs][crypt].

## Charm Accounts

Authentication is based on SSH keys, so account creation and authentication is invisible and frictionless. If a user already has Charm keys, we authenticate with them. If not, we create new ones.

Users can link multiple machines to their account, and linked machines will seamlessly gain access to their data.

### Backups

Use `charm backup-keys` to backup your account keys. Recover with `charm import-keys charm-keys-backup.tar`.

## CLI Client

The `charm` binary includes easy access to library functionality:

```bash
# Link a machine to your account
charm link

# Set a value
charm kv set weather humid

# Print out a tree of your files
charm fs tree /

# Encrypt something
charm crypt encrypt < secret.jpg > encrypted.jpg.json

# For more info
charm help
```

### Client Settings

Configure the client using environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `CHARM_HOST` | `charm.2389.dev` | Server hostname |
| `CHARM_SSH_PORT` | `35353` | SSH port |
| `CHARM_HTTP_PORT` | `35354` | HTTP port |
| `CHARM_DEBUG` | `false` | Enable debug logs |
| `CHARM_LOGFILE` | | Debug log file path |
| `CHARM_KEY_TYPE` | `ed25519` | Key type for new users |
| `CHARM_DATA_DIR` | | User data storage path |
| `CHARM_IDENTITY_KEY` | | Identity key path |

## Self-Hosting

Run your own instance:

```bash
charm serve
```

### Server Settings

| Variable | Default | Description |
|----------|---------|-------------|
| `CHARM_SERVER_BIND_ADDRESS` | `0.0.0.0` | Network interface |
| `CHARM_SERVER_HOST` | `localhost` | Hostname to advertise |
| `CHARM_SERVER_SSH_PORT` | `35353` | SSH port |
| `CHARM_SERVER_HTTP_PORT` | `35354` | HTTP port |
| `CHARM_SERVER_STATS_PORT` | `35355` | Stats port |
| `CHARM_SERVER_HEALTH_PORT` | `35356` | Health port |
| `CHARM_SERVER_DATA_DIR` | `./data` | Data directory |
| `CHARM_SERVER_USE_TLS` | `false` | Enable TLS |
| `CHARM_SERVER_TLS_KEY_FILE` | | TLS key file |
| `CHARM_SERVER_TLS_CERT_FILE` | | TLS cert file |
| `CHARM_SERVER_PUBLIC_URL` | | Public URL (for reverse proxy) |
| `CHARM_SERVER_ENABLE_METRICS` | `false` | Enable Prometheus metrics |
| `CHARM_SERVER_USER_MAX_STORAGE` | `0` | Max storage per user (0 = unlimited) |

See [Docker docs](docker.md) for containerized deployment.

## License

[MIT](LICENSE)

---

Based on [Charm](https://github.com/charmbracelet/charm) by [Charmbracelet](https://charm.sh).

[kv]: kv/
[fs]: fs/
[crypt]: crypt/
[badger]: https://github.com/dgraph-io/badger
