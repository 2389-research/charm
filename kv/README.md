# Charm KV

A Charm Cloud backed key-value store with automatic sync and encryption. Built on SQLite for reliability and simplicity.

## Features

- Cloud sync with automatic backup to Charm Cloud
- All data encrypted locally using Charm encryption keys
- Simple, clean API
- Read-only fallback mode when database is locked
- SQLite-based for durability and ACID guarantees

## Example

```go
package main

import (
	"fmt"
	"log"

	"github.com/charmbracelet/charm/client"
	"github.com/charmbracelet/charm/kv"
)

func main() {
	// Create a Charm client
	cc, err := client.NewClientWithDefaults()
	if err != nil {
		log.Fatal(err)
	}

	// Open a KV store with the name "myapp"
	db, err := kv.Open(cc, "myapp")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Sync latest updates from Charm Cloud
	if err := db.Sync(); err != nil {
		log.Printf("sync failed: %v", err)
	}

	// Set a value
	if err := db.Set([]byte("dog"), []byte("food")); err != nil {
		log.Fatal(err)
	}

	// Get a value
	v, err := db.Get([]byte("dog"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("got value: %s\n", string(v))

	// List all keys
	keys, err := db.Keys()
	if err != nil {
		log.Fatal(err)
	}
	for _, key := range keys {
		fmt.Printf("key: %s\n", string(key))
	}

	// Delete a key
	if err := db.Delete([]byte("dog")); err != nil {
		log.Fatal(err)
	}
}
```

## API Overview

### Opening a Database

```go
// Standard open
db, err := kv.Open(cc, "dbname")

// Open with fallback to read-only if locked
db, err := kv.OpenWithFallback(cc, "dbname")

// Check if in read-only mode
if db.IsReadOnly() {
	log.Println("Database opened in read-only mode")
}
```

### Basic Operations

```go
// Set a value
err := db.Set([]byte("key"), []byte("value"))

// Get a value
value, err := db.Get([]byte("key"))

// Delete a key
err := db.Delete([]byte("key"))

// List all keys
keys, err := db.Keys()
```

### Cloud Sync

```go
// Sync with Charm Cloud (download updates and upload local changes)
err := db.Sync()
```

### Cleanup

```go
// Always close when done
err := db.Close()
```

## Deleting a Database

1. Find the database in `charm fs ls /`
2. Delete the database with `charm fs rm db-name`
3. Locate the local copy of the database. To see where your charm-related data lives, run `charm` to start up with GUI, then select `Backup`
4. Run `rm ~/path/to/cloud.charm.sh/kv/db-name` to remove the local copy of your charm-kv database
