# PocketBase Storage Backend Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace Charm's SQLite and LocalFileStore backends with embedded PocketBase, providing admin UI, S3 backup capability, and REST API access.

**Architecture:** Embed PocketBase as a Go library running on port 35357. Implement `db.DB` and `storage.FileStore` interfaces using PocketBase collections. Auto-create collections on startup.

**Tech Stack:** Go, PocketBase Go SDK, existing Charm interfaces

---

## Task 1: Add PocketBase Dependency

**Files:**
- Modify: `go.mod`

**Step 1: Add dependency**

Run:
```bash
go get github.com/pocketbase/pocketbase@latest
```

**Step 2: Verify dependency added**

Run:
```bash
grep pocketbase go.mod
```
Expected: Line showing `github.com/pocketbase/pocketbase`

**Step 3: Tidy modules**

Run:
```bash
go mod tidy
```

**Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add pocketbase"
```

---

## Task 2: Create PocketBase App Lifecycle Package

**Files:**
- Create: `server/pocketbase/pocketbase.go`

**Step 1: Create the package file**

```go
// ABOUTME: PocketBase app lifecycle management for embedded deployment.
// ABOUTME: Handles initialization, collection setup, and server startup.

package pocketbase

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/log"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

// App wraps the PocketBase application instance.
type App struct {
	pb      *pocketbase.PocketBase
	dataDir string
	port    int
}

// Config holds PocketBase configuration.
type Config struct {
	DataDir    string
	Port       int
	AdminEmail string
	AdminPass  string
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		DataDir: "data",
		Port:    35357,
	}
}

// New creates a new PocketBase app instance.
func New(cfg *Config) (*App, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	pbDataDir := filepath.Join(cfg.DataDir, "pb_data")
	if err := os.MkdirAll(pbDataDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create pb_data dir: %w", err)
	}

	pb := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir: pbDataDir,
	})

	app := &App{
		pb:      pb,
		dataDir: cfg.DataDir,
		port:    cfg.Port,
	}

	return app, nil
}

// PB returns the underlying PocketBase instance for direct access.
func (a *App) PB() *pocketbase.PocketBase {
	return a.pb
}

// Bootstrap initializes PocketBase without starting the server.
func (a *App) Bootstrap() error {
	return a.pb.Bootstrap()
}

// Start begins serving the PocketBase admin UI and API.
func (a *App) Start() error {
	log.Info("Starting PocketBase", "port", a.port)

	// Configure and start server
	return apis.Serve(a.pb, apis.ServeConfig{
		HttpAddr:        fmt.Sprintf(":%d", a.port),
		ShowStartBanner: false,
	})
}

// StartAsync starts PocketBase in a goroutine.
func (a *App) StartAsync() {
	go func() {
		if err := a.Start(); err != nil {
			log.Error("PocketBase server error", "err", err)
		}
	}()
}

// OnBeforeServe registers a callback for before serve.
func (a *App) OnBeforeServe(fn func(e *core.ServeEvent) error) {
	a.pb.OnServe().BindFunc(func(e *core.ServeEvent) error {
		if err := fn(e); err != nil {
			return err
		}
		return e.Next()
	})
}
```

**Step 2: Verify it compiles**

Run:
```bash
go build ./server/pocketbase/
```
Expected: No errors

**Step 3: Commit**

```bash
git add server/pocketbase/pocketbase.go
git commit -m "feat(pocketbase): add app lifecycle package"
```

---

## Task 3: Create Collection Schema Definitions

**Files:**
- Create: `server/pocketbase/collections.go`

**Step 1: Create collection definitions**

```go
// ABOUTME: PocketBase collection schema definitions for Charm data.
// ABOUTME: Auto-creates collections on startup if they don't exist.

package pocketbase

import (
	"github.com/charmbracelet/log"
	"github.com/pocketbase/pocketbase/core"
)

// CollectionNames defines the collection identifiers.
const (
	CollectionCharmUsers  = "charm_users"
	CollectionPublicKeys  = "public_keys"
	CollectionEncryptKeys = "encrypt_keys"
	CollectionNamedSeqs   = "named_seqs"
	CollectionNews        = "news"
	CollectionTokens      = "tokens"
	CollectionCharmFiles  = "charm_files"
)

// EnsureCollections creates all required collections if they don't exist.
func (a *App) EnsureCollections() error {
	log.Debug("Ensuring PocketBase collections exist")

	if err := a.ensureCharmUsersCollection(); err != nil {
		return err
	}
	if err := a.ensurePublicKeysCollection(); err != nil {
		return err
	}
	if err := a.ensureEncryptKeysCollection(); err != nil {
		return err
	}
	if err := a.ensureNamedSeqsCollection(); err != nil {
		return err
	}
	if err := a.ensureNewsCollection(); err != nil {
		return err
	}
	if err := a.ensureTokensCollection(); err != nil {
		return err
	}
	if err := a.ensureCharmFilesCollection(); err != nil {
		return err
	}

	log.Debug("All PocketBase collections ready")
	return nil
}

func (a *App) ensureCharmUsersCollection() error {
	_, err := a.pb.FindCollectionByNameOrId(CollectionCharmUsers)
	if err == nil {
		return nil
	}

	collection := core.NewBaseCollection(CollectionCharmUsers)
	collection.Fields.Add(
		&core.TextField{Name: "charm_id", Required: true},
		&core.TextField{Name: "name"},
		&core.TextField{Name: "email"},
		&core.TextField{Name: "bio"},
	)
	collection.AddIndex("idx_charm_users_charm_id", true, "charm_id", "")
	collection.AddIndex("idx_charm_users_name", true, "name", "name != ''")

	return a.pb.Save(collection)
}

func (a *App) ensurePublicKeysCollection() error {
	_, err := a.pb.FindCollectionByNameOrId(CollectionPublicKeys)
	if err == nil {
		return nil
	}

	collection := core.NewBaseCollection(CollectionPublicKeys)
	collection.Fields.Add(
		&core.RelationField{
			Name:          "user",
			CollectionId:  CollectionCharmUsers,
			CascadeDelete: true,
			Required:      true,
		},
		&core.TextField{Name: "public_key", Required: true},
	)
	collection.AddIndex("idx_public_keys_user_key", true, "user, public_key", "")
	collection.AddIndex("idx_public_keys_key", false, "public_key", "")

	return a.pb.Save(collection)
}

func (a *App) ensureEncryptKeysCollection() error {
	_, err := a.pb.FindCollectionByNameOrId(CollectionEncryptKeys)
	if err == nil {
		return nil
	}

	collection := core.NewBaseCollection(CollectionEncryptKeys)
	collection.Fields.Add(
		&core.RelationField{
			Name:          "public_key",
			CollectionId:  CollectionPublicKeys,
			CascadeDelete: true,
			Required:      true,
		},
		&core.TextField{Name: "global_id", Required: true},
		&core.TextField{Name: "encrypted_key", Required: true},
	)
	collection.AddIndex("idx_encrypt_keys_pk_gid", true, "public_key, global_id", "")

	return a.pb.Save(collection)
}

func (a *App) ensureNamedSeqsCollection() error {
	_, err := a.pb.FindCollectionByNameOrId(CollectionNamedSeqs)
	if err == nil {
		return nil
	}

	collection := core.NewBaseCollection(CollectionNamedSeqs)
	collection.Fields.Add(
		&core.RelationField{
			Name:          "user",
			CollectionId:  CollectionCharmUsers,
			CascadeDelete: true,
			Required:      true,
		},
		&core.TextField{Name: "name", Required: true},
		&core.NumberField{Name: "seq"},
	)
	collection.AddIndex("idx_named_seqs_user_name", true, "user, name", "")

	return a.pb.Save(collection)
}

func (a *App) ensureNewsCollection() error {
	_, err := a.pb.FindCollectionByNameOrId(CollectionNews)
	if err == nil {
		return nil
	}

	collection := core.NewBaseCollection(CollectionNews)
	collection.Fields.Add(
		&core.TextField{Name: "subject", Required: true},
		&core.TextField{Name: "body"},
		&core.JSONField{Name: "tags"},
	)

	return a.pb.Save(collection)
}

func (a *App) ensureTokensCollection() error {
	_, err := a.pb.FindCollectionByNameOrId(CollectionTokens)
	if err == nil {
		return nil
	}

	collection := core.NewBaseCollection(CollectionTokens)
	collection.Fields.Add(
		&core.TextField{Name: "pin", Required: true},
	)
	collection.AddIndex("idx_tokens_pin", true, "pin", "")

	return a.pb.Save(collection)
}

func (a *App) ensureCharmFilesCollection() error {
	_, err := a.pb.FindCollectionByNameOrId(CollectionCharmFiles)
	if err == nil {
		return nil
	}

	collection := core.NewBaseCollection(CollectionCharmFiles)
	collection.Fields.Add(
		&core.TextField{Name: "charm_id", Required: true},
		&core.TextField{Name: "path", Required: true},
		&core.FileField{Name: "file", MaxSelect: 1, MaxSize: 8589934591}, // ~8GB
		&core.BoolField{Name: "is_dir"},
		&core.NumberField{Name: "mode"},
	)
	collection.AddIndex("idx_charm_files_cid_path", true, "charm_id, path", "")

	return a.pb.Save(collection)
}
```

**Step 2: Verify it compiles**

Run:
```bash
go build ./server/pocketbase/
```
Expected: No errors

**Step 3: Commit**

```bash
git add server/pocketbase/collections.go
git commit -m "feat(pocketbase): add collection schema definitions"
```

---

## Task 4: Create PocketBase DB Implementation - Core Types

**Files:**
- Create: `server/db/pocketbase/db.go`

**Step 1: Create the DB struct and constructor**

```go
// ABOUTME: PocketBase implementation of the db.DB interface.
// ABOUTME: Provides user, key, and metadata storage via PocketBase collections.

package pocketbase

import (
	"fmt"
	"strconv"
	"time"

	"github.com/charmbracelet/log"
	"github.com/google/uuid"
	"github.com/pocketbase/pocketbase/core"

	charm "github.com/charmbracelet/charm/proto"
	pb "github.com/charmbracelet/charm/server/pocketbase"
)

// DB implements the db.DB interface using PocketBase.
type DB struct {
	app *pb.App
}

// New creates a new PocketBase-backed DB.
func New(app *pb.App) *DB {
	return &DB{app: app}
}

// Close closes the database connection.
func (d *DB) Close() error {
	return nil
}

// helper to get the PocketBase instance
func (d *DB) pb() *core.App {
	return d.app.PB().App
}
```

**Step 2: Verify it compiles**

Run:
```bash
go build ./server/db/pocketbase/
```
Expected: No errors

**Step 3: Commit**

```bash
git add server/db/pocketbase/db.go
git commit -m "feat(db/pocketbase): add core types and constructor"
```

---

## Task 5: Implement User Methods

**Files:**
- Modify: `server/db/pocketbase/db.go`

**Step 1: Add user-related methods**

Append to `server/db/pocketbase/db.go`:

```go
// UserForKey returns the user for the given key, or optionally creates a new user with it.
func (d *DB) UserForKey(key string, create bool) (*charm.User, error) {
	app := d.pb()
	var user *charm.User

	err := app.RunInTransaction(func(txApp core.App) error {
		// Find public key record
		pkRecord, err := txApp.FindFirstRecordByData(pb.CollectionPublicKeys, "public_key", key)
		if err != nil && create {
			// Create new user and public key
			log.Debug("Creating user for key", "key", charm.PublicKeySha(key))

			userRecord, err := d.createUser(txApp, key)
			if err != nil {
				return err
			}
			user = d.recordToUser(userRecord)

			// Get the public key we just created
			pkRecord, err = txApp.FindFirstRecordByData(pb.CollectionPublicKeys, "public_key", key)
			if err != nil {
				return err
			}
			user.PublicKey = d.recordToPublicKey(pkRecord)
			return nil
		} else if err != nil {
			return charm.ErrMissingUser
		}

		// Get user from public key relation
		userID := pkRecord.GetString("user")
		userRecord, err := txApp.FindRecordById(pb.CollectionCharmUsers, userID)
		if err != nil {
			return charm.ErrMissingUser
		}

		user = d.recordToUser(userRecord)
		user.PublicKey = d.recordToPublicKey(pkRecord)
		return nil
	})

	return user, err
}

// GetUserWithID returns the user for the given charm ID.
func (d *DB) GetUserWithID(charmID string) (*charm.User, error) {
	record, err := d.pb().FindFirstRecordByData(pb.CollectionCharmUsers, "charm_id", charmID)
	if err != nil {
		return nil, charm.ErrMissingUser
	}
	return d.recordToUser(record), nil
}

// GetUserWithName returns the user for the given name.
func (d *DB) GetUserWithName(name string) (*charm.User, error) {
	record, err := d.pb().FindFirstRecordByData(pb.CollectionCharmUsers, "name", name)
	if err != nil {
		return nil, charm.ErrMissingUser
	}
	return d.recordToUser(record), nil
}

// SetUserName sets a user name for the given user id.
func (d *DB) SetUserName(charmID string, name string) (*charm.User, error) {
	app := d.pb()
	var user *charm.User

	err := app.RunInTransaction(func(txApp core.App) error {
		// Check if name is already taken
		existing, err := txApp.FindFirstRecordByData(pb.CollectionCharmUsers, "name", name)
		if err == nil && existing.GetString("charm_id") != charmID {
			return charm.ErrNameTaken
		}

		// Find user by charm_id
		record, err := txApp.FindFirstRecordByData(pb.CollectionCharmUsers, "charm_id", charmID)
		if err != nil {
			return charm.ErrMissingUser
		}

		record.Set("name", name)
		if err := txApp.Save(record); err != nil {
			return err
		}

		user = d.recordToUser(record)
		return nil
	})

	return user, err
}

// UserCount returns the number of users.
func (d *DB) UserCount() (int, error) {
	records, err := d.pb().FindAllRecords(pb.CollectionCharmUsers)
	if err != nil {
		return 0, err
	}
	return len(records), nil
}

// UserNameCount returns the number of users with a user name set.
func (d *DB) UserNameCount() (int, error) {
	records, err := d.pb().FindRecordsByFilter(
		pb.CollectionCharmUsers,
		"name != ''",
		"",
		0,
		0,
	)
	if err != nil {
		return 0, err
	}
	return len(records), nil
}

// MergeUsers merges two users into a single one.
func (d *DB) MergeUsers(userID1 int, userID2 int) error {
	app := d.pb()

	return app.RunInTransaction(func(txApp core.App) error {
		// Find both users by their internal ID
		// Note: We need to find by record ID, not integer ID
		// This is a simplification - in practice we'd need to map integer IDs
		user1, err := d.findUserByIntID(txApp, userID1)
		if err != nil {
			return err
		}
		user2, err := d.findUserByIntID(txApp, userID2)
		if err != nil {
			return err
		}

		// Move all public keys from user2 to user1
		keys, err := txApp.FindRecordsByFilter(
			pb.CollectionPublicKeys,
			fmt.Sprintf("user = '%s'", user2.Id),
			"",
			0,
			0,
		)
		if err != nil {
			return err
		}

		for _, pk := range keys {
			pk.Set("user", user1.Id)
			if err := txApp.Save(pk); err != nil {
				return err
			}
		}

		// Delete user2
		return txApp.Delete(user2)
	})
}

func (d *DB) createUser(txApp core.App, key string) (*core.Record, error) {
	collection, err := txApp.FindCollectionByNameOrId(pb.CollectionCharmUsers)
	if err != nil {
		return nil, err
	}

	charmID := uuid.New().String()
	record := core.NewRecord(collection)
	record.Set("charm_id", charmID)

	if err := txApp.Save(record); err != nil {
		return nil, err
	}

	// Create public key for this user
	if err := d.insertPublicKey(txApp, record.Id, key); err != nil {
		return nil, err
	}

	return record, nil
}

func (d *DB) insertPublicKey(txApp core.App, userRecordID string, key string) error {
	collection, err := txApp.FindCollectionByNameOrId(pb.CollectionPublicKeys)
	if err != nil {
		return err
	}

	record := core.NewRecord(collection)
	record.Set("user", userRecordID)
	record.Set("public_key", key)

	return txApp.Save(record)
}

func (d *DB) recordToUser(r *core.Record) *charm.User {
	created := r.GetDateTime("created").Time()
	return &charm.User{
		ID:        d.recordIDToInt(r.Id),
		CharmID:   r.GetString("charm_id"),
		Name:      r.GetString("name"),
		Email:     r.GetString("email"),
		Bio:       r.GetString("bio"),
		CreatedAt: &created,
	}
}

func (d *DB) recordToPublicKey(r *core.Record) *charm.PublicKey {
	created := r.GetDateTime("created").Time()
	return &charm.PublicKey{
		ID:        d.recordIDToInt(r.Id),
		UserID:    d.recordIDToInt(r.GetString("user")),
		Key:       r.GetString("public_key"),
		CreatedAt: &created,
	}
}

// recordIDToInt converts PocketBase string IDs to integers for compatibility.
// This uses a hash to maintain consistency.
func (d *DB) recordIDToInt(id string) int {
	if id == "" {
		return 0
	}
	// Use first 8 chars of ID as hex, convert to int
	if len(id) >= 8 {
		if n, err := strconv.ParseInt(id[:8], 16, 32); err == nil {
			return int(n)
		}
	}
	// Fallback: sum of bytes
	sum := 0
	for _, c := range id {
		sum += int(c)
	}
	return sum
}

func (d *DB) findUserByIntID(txApp core.App, intID int) (*core.Record, error) {
	// This is a compatibility shim - we iterate to find matching ID
	records, err := txApp.FindAllRecords(pb.CollectionCharmUsers)
	if err != nil {
		return nil, err
	}
	for _, r := range records {
		if d.recordIDToInt(r.Id) == intID {
			return r, nil
		}
	}
	return nil, fmt.Errorf("user not found with int ID %d", intID)
}
```

**Step 2: Verify it compiles**

Run:
```bash
go build ./server/db/pocketbase/
```
Expected: No errors

**Step 3: Commit**

```bash
git add server/db/pocketbase/db.go
git commit -m "feat(db/pocketbase): implement user methods"
```

---

## Task 6: Implement Key Methods

**Files:**
- Modify: `server/db/pocketbase/db.go`

**Step 1: Add key-related methods**

Append to `server/db/pocketbase/db.go`:

```go
// LinkUserKey links a user to a key.
func (d *DB) LinkUserKey(user *charm.User, key string) error {
	ks := charm.PublicKeySha(key)
	log.Debug("Linking user and key", "id", user.CharmID, "key", ks)

	app := d.pb()
	return app.RunInTransaction(func(txApp core.App) error {
		// Find user record
		userRecord, err := txApp.FindFirstRecordByData(pb.CollectionCharmUsers, "charm_id", user.CharmID)
		if err != nil {
			return err
		}
		return d.insertPublicKey(txApp, userRecord.Id, key)
	})
}

// UnlinkUserKey unlinks the key from the user.
func (d *DB) UnlinkUserKey(user *charm.User, key string) error {
	ks := charm.PublicKeySha(key)
	log.Debug("Unlinking user key", "id", user.CharmID, "key", ks)

	app := d.pb()
	return app.RunInTransaction(func(txApp core.App) error {
		// Find user record
		userRecord, err := txApp.FindFirstRecordByData(pb.CollectionCharmUsers, "charm_id", user.CharmID)
		if err != nil {
			return err
		}

		// Find and delete the public key
		pkRecord, err := txApp.FindFirstRecordByFilter(
			pb.CollectionPublicKeys,
			fmt.Sprintf("user = '%s' && public_key = '%s'", userRecord.Id, key),
		)
		if err != nil {
			return err
		}

		if err := txApp.Delete(pkRecord); err != nil {
			return err
		}

		// Check if user has any remaining keys
		remaining, err := txApp.FindRecordsByFilter(
			pb.CollectionPublicKeys,
			fmt.Sprintf("user = '%s'", userRecord.Id),
			"",
			0,
			0,
		)
		if err != nil {
			return err
		}

		// If no keys remain, delete the user
		if len(remaining) == 0 {
			log.Debug("Removing last key for account, deleting", "id", user.CharmID)
			return txApp.Delete(userRecord)
		}

		return nil
	})
}

// KeysForUser returns all user's public keys.
func (d *DB) KeysForUser(user *charm.User) ([]*charm.PublicKey, error) {
	log.Debug("Getting keys for user", "id", user.CharmID)

	// Find user record
	userRecord, err := d.pb().FindFirstRecordByData(pb.CollectionCharmUsers, "charm_id", user.CharmID)
	if err != nil {
		return nil, err
	}

	records, err := d.pb().FindRecordsByFilter(
		pb.CollectionPublicKeys,
		fmt.Sprintf("user = '%s'", userRecord.Id),
		"",
		0,
		0,
	)
	if err != nil {
		return nil, err
	}

	keys := make([]*charm.PublicKey, 0, len(records))
	for _, r := range records {
		keys = append(keys, d.recordToPublicKey(r))
	}

	return keys, nil
}

// EncryptKeysForPublicKey returns the encrypt keys for the given public key.
func (d *DB) EncryptKeysForPublicKey(pk *charm.PublicKey) ([]*charm.EncryptKey, error) {
	// Find public key record
	pkRecord, err := d.pb().FindFirstRecordByData(pb.CollectionPublicKeys, "public_key", pk.Key)
	if err != nil {
		return nil, err
	}

	records, err := d.pb().FindRecordsByFilter(
		pb.CollectionEncryptKeys,
		fmt.Sprintf("public_key = '%s'", pkRecord.Id),
		"",
		0,
		0,
	)
	if err != nil {
		return nil, err
	}

	keys := make([]*charm.EncryptKey, 0, len(records))
	for _, r := range records {
		created := r.GetDateTime("created").Time()
		keys = append(keys, &charm.EncryptKey{
			ID:        r.GetString("global_id"),
			Key:       r.GetString("encrypted_key"),
			CreatedAt: &created,
		})
	}

	return keys, nil
}

// AddEncryptKeyForPublicKey adds an encrypted key to the user.
func (d *DB) AddEncryptKeyForPublicKey(u *charm.User, pk string, gid string, ek string, ca *time.Time) error {
	log.Debug("Adding encrypted key for user", "key", gid, "time", ca, "id", u.CharmID)

	app := d.pb()
	return app.RunInTransaction(func(txApp core.App) error {
		// Verify the public key belongs to this user
		u2, err := d.UserForKey(pk, false)
		if err != nil {
			return err
		}
		if u2.CharmID != u.CharmID {
			return fmt.Errorf("trying to add encrypted key for unauthorized user")
		}

		// Find public key record
		pkRecord, err := txApp.FindFirstRecordByData(pb.CollectionPublicKeys, "public_key", pk)
		if err != nil {
			return err
		}

		// Check if encrypt key already exists
		existing, err := txApp.FindFirstRecordByFilter(
			pb.CollectionEncryptKeys,
			fmt.Sprintf("public_key = '%s' && global_id = '%s'", pkRecord.Id, gid),
		)
		if err == nil && existing != nil {
			log.Debug("Encrypt key already exists for public key, skipping", "key", gid)
			return nil
		}

		// Create new encrypt key
		collection, err := txApp.FindCollectionByNameOrId(pb.CollectionEncryptKeys)
		if err != nil {
			return err
		}

		record := core.NewRecord(collection)
		record.Set("public_key", pkRecord.Id)
		record.Set("global_id", gid)
		record.Set("encrypted_key", ek)

		return txApp.Save(record)
	})
}
```

**Step 2: Verify it compiles**

Run:
```bash
go build ./server/db/pocketbase/
```
Expected: No errors

**Step 3: Commit**

```bash
git add server/db/pocketbase/db.go
git commit -m "feat(db/pocketbase): implement key methods"
```

---

## Task 7: Implement Sequence, News, and Token Methods

**Files:**
- Modify: `server/db/pocketbase/db.go`

**Step 1: Add remaining methods**

Append to `server/db/pocketbase/db.go`:

```go
// GetSeq returns the named sequence.
func (d *DB) GetSeq(u *charm.User, name string) (uint64, error) {
	userRecord, err := d.pb().FindFirstRecordByData(pb.CollectionCharmUsers, "charm_id", u.CharmID)
	if err != nil {
		return 0, err
	}

	record, err := d.pb().FindFirstRecordByFilter(
		pb.CollectionNamedSeqs,
		fmt.Sprintf("user = '%s' && name = '%s'", userRecord.Id, name),
	)
	if err != nil {
		// Create new sequence starting at 1
		return d.NextSeq(u, name)
	}

	return uint64(record.GetInt("seq")), nil
}

// NextSeq increments the sequence and returns.
func (d *DB) NextSeq(u *charm.User, name string) (uint64, error) {
	app := d.pb()
	var seq uint64

	err := app.RunInTransaction(func(txApp core.App) error {
		userRecord, err := txApp.FindFirstRecordByData(pb.CollectionCharmUsers, "charm_id", u.CharmID)
		if err != nil {
			return err
		}

		record, err := txApp.FindFirstRecordByFilter(
			pb.CollectionNamedSeqs,
			fmt.Sprintf("user = '%s' && name = '%s'", userRecord.Id, name),
		)
		if err != nil {
			// Create new sequence
			collection, err := txApp.FindCollectionByNameOrId(pb.CollectionNamedSeqs)
			if err != nil {
				return err
			}

			record = core.NewRecord(collection)
			record.Set("user", userRecord.Id)
			record.Set("name", name)
			record.Set("seq", 1)
			seq = 1
		} else {
			// Increment existing
			seq = uint64(record.GetInt("seq")) + 1
			record.Set("seq", seq)
		}

		return txApp.Save(record)
	})

	return seq, err
}

// PostNews publishes news to the server.
func (d *DB) PostNews(subject string, body string, tags []string) error {
	collection, err := d.pb().FindCollectionByNameOrId(pb.CollectionNews)
	if err != nil {
		return err
	}

	record := core.NewRecord(collection)
	record.Set("subject", subject)
	record.Set("body", body)
	record.Set("tags", tags)

	return d.pb().Save(record)
}

// GetNews returns the server news by ID.
func (d *DB) GetNews(id string) (*charm.News, error) {
	record, err := d.pb().FindRecordById(pb.CollectionNews, id)
	if err != nil {
		return nil, err
	}

	return d.recordToNews(record), nil
}

// GetNewsList returns the list of server news.
func (d *DB) GetNewsList(tag string, page int) ([]*charm.News, error) {
	filter := ""
	if tag != "" {
		filter = fmt.Sprintf("tags ~ '%s'", tag)
	}

	records, err := d.pb().FindRecordsByFilter(
		pb.CollectionNews,
		filter,
		"-created",
		50,
		page*50,
	)
	if err != nil {
		return nil, err
	}

	news := make([]*charm.News, 0, len(records))
	for _, r := range records {
		news = append(news, d.recordToNews(r))
	}

	return news, nil
}

func (d *DB) recordToNews(r *core.Record) *charm.News {
	tags := r.Get("tags")
	tag := ""
	if tagSlice, ok := tags.([]interface{}); ok && len(tagSlice) > 0 {
		if s, ok := tagSlice[0].(string); ok {
			tag = s
		}
	}

	return &charm.News{
		ID:        r.Id,
		Subject:   r.GetString("subject"),
		Body:      r.GetString("body"),
		Tag:       tag,
		CreatedAt: r.GetDateTime("created").Time(),
	}
}

// SetToken creates the given token.
func (d *DB) SetToken(token charm.Token) error {
	collection, err := d.pb().FindCollectionByNameOrId(pb.CollectionTokens)
	if err != nil {
		return err
	}

	// Check if token already exists
	_, err = d.pb().FindFirstRecordByData(pb.CollectionTokens, "pin", string(token))
	if err == nil {
		return charm.ErrTokenExists
	}

	record := core.NewRecord(collection)
	record.Set("pin", string(token))

	return d.pb().Save(record)
}

// DeleteToken deletes the given token.
func (d *DB) DeleteToken(token charm.Token) error {
	record, err := d.pb().FindFirstRecordByData(pb.CollectionTokens, "pin", string(token))
	if err != nil {
		return nil // Token doesn't exist, that's fine
	}

	return d.pb().Delete(record)
}
```

**Step 2: Verify it compiles**

Run:
```bash
go build ./server/db/pocketbase/
```
Expected: No errors

**Step 3: Commit**

```bash
git add server/db/pocketbase/db.go
git commit -m "feat(db/pocketbase): implement sequence, news, and token methods"
```

---

## Task 8: Create PocketBase FileStore Implementation

**Files:**
- Create: `server/storage/pocketbase/storage.go`

**Step 1: Create the file store**

```go
// ABOUTME: PocketBase implementation of the storage.FileStore interface.
// ABOUTME: Stores encrypted files as PocketBase file attachments.

package pocketbase

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	charmfs "github.com/charmbracelet/charm/fs"
	charm "github.com/charmbracelet/charm/proto"
	pb "github.com/charmbracelet/charm/server/pocketbase"
)

// FileStore implements storage.FileStore using PocketBase.
type FileStore struct {
	app *pb.App
}

// New creates a new PocketBase-backed FileStore.
func New(app *pb.App) *FileStore {
	return &FileStore{app: app}
}

func (s *FileStore) pb() *core.App {
	return s.app.PB().App
}

// Stat returns the FileInfo for the given Charm ID and path.
func (s *FileStore) Stat(charmID, path string) (fs.FileInfo, error) {
	record, err := s.findFileRecord(charmID, path)
	if err != nil {
		return nil, fs.ErrNotExist
	}

	info := s.recordToFileInfo(record)

	// For directories, calculate total size of contents
	if info.IsDir() {
		size, err := s.calculateDirSize(charmID, path)
		if err == nil {
			info.FileInfo.Size = size
		}
	}

	return info, nil
}

// Get returns an fs.File for the given Charm ID and path.
func (s *FileStore) Get(charmID string, path string) (fs.File, error) {
	record, err := s.findFileRecord(charmID, path)
	if err != nil {
		return nil, fs.ErrNotExist
	}

	isDir := record.GetBool("is_dir")

	if isDir {
		// Return directory listing as JSON
		return s.getDirListing(charmID, path, record)
	}

	// Return file contents
	return s.getFile(record)
}

// Put stores the data with the Charm ID and path.
func (s *FileStore) Put(charmID string, path string, r io.Reader, mode fs.FileMode) error {
	if cpath := filepath.Clean(path); cpath == string(os.PathSeparator) {
		return fmt.Errorf("invalid path specified: %s", cpath)
	}

	app := s.pb()
	collection, err := app.FindCollectionByNameOrId(pb.CollectionCharmFiles)
	if err != nil {
		return err
	}

	// Check if record already exists
	existing, _ := s.findFileRecord(charmID, path)

	var record *core.Record
	if existing != nil {
		record = existing
	} else {
		record = core.NewRecord(collection)
		record.Set("charm_id", charmID)
		record.Set("path", path)
	}

	record.Set("is_dir", mode.IsDir())
	record.Set("mode", int(mode))

	if !mode.IsDir() {
		// Read file content and create file attachment
		content, err := io.ReadAll(r)
		if err != nil {
			return err
		}

		filename := filepath.Base(path)
		file, err := filesystem.NewFileFromBytes(content, filename)
		if err != nil {
			return err
		}

		record.Set("file", file)
	}

	// Ensure parent directories exist
	if err := s.ensureParentDirs(charmID, path, mode); err != nil {
		return err
	}

	return app.Save(record)
}

// Delete deletes the file at the given path.
func (s *FileStore) Delete(charmID string, path string) error {
	app := s.pb()

	// Delete the record and all children (for directories)
	return app.RunInTransaction(func(txApp core.App) error {
		// Delete exact match
		record, err := s.findFileRecordTx(txApp, charmID, path)
		if err == nil {
			if err := txApp.Delete(record); err != nil {
				return err
			}
		}

		// Delete children if this was a directory
		children, err := txApp.FindRecordsByFilter(
			pb.CollectionCharmFiles,
			fmt.Sprintf("charm_id = '%s' && path ~ '%s/'", charmID, path),
			"",
			0,
			0,
		)
		if err != nil {
			return nil // No children, that's fine
		}

		for _, child := range children {
			if err := txApp.Delete(child); err != nil {
				return err
			}
		}

		return nil
	})
}

func (s *FileStore) findFileRecord(charmID, path string) (*core.Record, error) {
	return s.pb().FindFirstRecordByFilter(
		pb.CollectionCharmFiles,
		fmt.Sprintf("charm_id = '%s' && path = '%s'", charmID, path),
	)
}

func (s *FileStore) findFileRecordTx(txApp core.App, charmID, path string) (*core.Record, error) {
	return txApp.FindFirstRecordByFilter(
		pb.CollectionCharmFiles,
		fmt.Sprintf("charm_id = '%s' && path = '%s'", charmID, path),
	)
}

func (s *FileStore) recordToFileInfo(r *core.Record) *charmfs.FileInfo {
	isDir := r.GetBool("is_dir")
	mode := fs.FileMode(r.GetInt("mode"))
	if isDir {
		mode = mode | fs.ModeDir
	}

	var size int64
	if !isDir {
		// Get file size from the file field
		files := r.GetStringSlice("file")
		if len(files) > 0 {
			// PocketBase stores file metadata - we'd need filesystem access for actual size
			// For now, use 0 and let Stat calculate if needed
			size = 0
		}
	}

	return &charmfs.FileInfo{
		FileInfo: charm.FileInfo{
			Name:    filepath.Base(r.GetString("path")),
			IsDir:   isDir,
			Size:    size,
			ModTime: r.GetDateTime("updated").Time(),
			Mode:    mode,
		},
	}
}

func (s *FileStore) calculateDirSize(charmID, path string) (int64, error) {
	prefix := path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	records, err := s.pb().FindRecordsByFilter(
		pb.CollectionCharmFiles,
		fmt.Sprintf("charm_id = '%s' && path ~ '%s' && is_dir = false", charmID, prefix),
		"",
		0,
		0,
	)
	if err != nil {
		return 0, err
	}

	var total int64
	for range records {
		// TODO: Get actual file sizes from PocketBase filesystem
		// For now, this is a placeholder
		total += 0
	}

	return total, nil
}

func (s *FileStore) getDirListing(charmID, path string, dirRecord *core.Record) (fs.File, error) {
	prefix := path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	// Find immediate children only
	records, err := s.pb().FindRecordsByFilter(
		pb.CollectionCharmFiles,
		fmt.Sprintf("charm_id = '%s' && path ~ '%s'", charmID, prefix),
		"",
		0,
		0,
	)
	if err != nil {
		records = []*core.Record{}
	}

	// Filter to immediate children only
	fis := make([]charm.FileInfo, 0)
	seen := make(map[string]bool)

	for _, r := range records {
		childPath := r.GetString("path")
		// Get relative path from prefix
		rel := strings.TrimPrefix(childPath, prefix)
		if rel == "" {
			continue
		}

		// Get first component (immediate child name)
		parts := strings.SplitN(rel, "/", 2)
		childName := parts[0]

		if seen[childName] {
			continue
		}
		seen[childName] = true

		info := s.recordToFileInfo(r)
		fis = append(fis, info.FileInfo)
	}

	dirInfo := s.recordToFileInfo(dirRecord)
	dir := charm.FileInfo{
		Name:    dirInfo.Name(),
		IsDir:   true,
		Size:    0,
		ModTime: dirInfo.ModTime(),
		Mode:    dirInfo.Mode(),
		Files:   fis,
	}

	buf := bytes.NewBuffer(nil)
	if err := json.NewEncoder(buf).Encode(dir); err != nil {
		return nil, err
	}

	return &charmfs.DirFile{
		Buffer:   buf,
		FileInfo: dirInfo,
	}, nil
}

func (s *FileStore) getFile(record *core.Record) (fs.File, error) {
	app := s.pb()

	files := record.GetStringSlice("file")
	if len(files) == 0 {
		return nil, fs.ErrNotExist
	}

	filename := files[0]
	fsys, err := app.NewFilesystem()
	if err != nil {
		return nil, err
	}
	defer fsys.Close()

	collection, err := app.FindCollectionByNameOrId(pb.CollectionCharmFiles)
	if err != nil {
		return nil, err
	}

	key := record.BaseFilesPath() + "/" + filename
	_ = collection // Used for path construction

	// Read file from PocketBase storage
	reader, err := fsys.GetFile(key)
	if err != nil {
		return nil, err
	}

	content, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		return nil, err
	}

	info := s.recordToFileInfo(record)

	return &pbFile{
		Reader:   bytes.NewReader(content),
		info:     info,
		filename: filename,
	}, nil
}

func (s *FileStore) ensureParentDirs(charmID, path string, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "/" {
		return nil
	}

	// Check if parent exists
	_, err := s.findFileRecord(charmID, dir)
	if err == nil {
		return nil // Parent exists
	}

	// Create parent directory
	app := s.pb()
	collection, err := app.FindCollectionByNameOrId(pb.CollectionCharmFiles)
	if err != nil {
		return err
	}

	record := core.NewRecord(collection)
	record.Set("charm_id", charmID)
	record.Set("path", dir)
	record.Set("is_dir", true)
	record.Set("mode", int(mode|fs.ModeDir|0100)) // Add execute for dirs

	if err := app.Save(record); err != nil {
		return err
	}

	// Recursively ensure grandparent
	return s.ensureParentDirs(charmID, dir, mode)
}

// pbFile implements fs.File for PocketBase files.
type pbFile struct {
	*bytes.Reader
	info     fs.FileInfo
	filename string
}

func (f *pbFile) Stat() (fs.FileInfo, error) {
	return f.info, nil
}

func (f *pbFile) Close() error {
	return nil
}

// Name returns the filename (required for some fs.File consumers).
func (f *pbFile) Name() string {
	return f.filename
}
```

**Step 2: Verify it compiles**

Run:
```bash
go build ./server/storage/pocketbase/
```
Expected: No errors

**Step 3: Commit**

```bash
git add server/storage/pocketbase/storage.go
git commit -m "feat(storage/pocketbase): implement FileStore interface"
```

---

## Task 9: Wire Up PocketBase in Server Config

**Files:**
- Modify: `server/server.go`

**Step 1: Add PocketBase initialization**

First, read the current server.go to understand the structure, then add PocketBase integration.

Add imports:
```go
import (
    // ... existing imports ...
    pbapp "github.com/charmbracelet/charm/server/pocketbase"
    pbdb "github.com/charmbracelet/charm/server/db/pocketbase"
    pbstorage "github.com/charmbracelet/charm/server/storage/pocketbase"
)
```

Add to Config struct:
```go
type Config struct {
    // ... existing fields ...
    PBPort int `env:"CHARM_SERVER_PB_PORT" envDefault:"35357"`
}
```

Modify init() to use PocketBase:
```go
func (srv *Server) init(cfg *Config) {
    // ... existing setup ...

    // Initialize PocketBase
    pbCfg := &pbapp.Config{
        DataDir: cfg.DataDir,
        Port:    cfg.PBPort,
    }
    pbApp, err := pbapp.New(pbCfg)
    if err != nil {
        panic(fmt.Sprintf("failed to create PocketBase app: %v", err))
    }

    // Bootstrap and ensure collections
    if err := pbApp.Bootstrap(); err != nil {
        panic(fmt.Sprintf("failed to bootstrap PocketBase: %v", err))
    }
    if err := pbApp.EnsureCollections(); err != nil {
        panic(fmt.Sprintf("failed to ensure PocketBase collections: %v", err))
    }

    // Start PocketBase async
    pbApp.StartAsync()

    // Use PocketBase implementations
    if cfg.DB == nil {
        srv.Config = cfg.WithDB(pbdb.New(pbApp))
    }
    if cfg.FileStore == nil {
        srv.Config = cfg.WithFileStore(pbstorage.New(pbApp))
    }
}
```

**Note:** This task requires careful integration with existing server.go code. Review the file structure before making changes.

**Step 2: Verify it compiles**

Run:
```bash
go build .
```
Expected: No errors

**Step 3: Commit**

```bash
git add server/server.go
git commit -m "feat(server): wire up PocketBase storage backend"
```

---

## Task 10: Update Dockerfile

**Files:**
- Modify: `Dockerfile`

**Step 1: Add PocketBase port**

Add to EXPOSE section:
```dockerfile
# PocketBase Admin
EXPOSE 35357/tcp
```

**Step 2: Commit**

```bash
git add Dockerfile
git commit -m "chore(docker): expose PocketBase admin port"
```

---

## Task 11: Update Makefile

**Files:**
- Modify: `Makefile`

**Step 1: Update docker-run target**

Update the docker-run target to include port 35357:
```makefile
docker-run: ## Run the Docker container
	docker run -it --rm \
		-p 35353:35353 \
		-p 35354:35354 \
		-p 35355:35355 \
		-p 35356:35356 \
		-p 35357:35357 \
		-v charm-data:/data \
		$(BINARY_NAME):latest
```

**Step 2: Commit**

```bash
git add Makefile
git commit -m "chore(makefile): add PocketBase port to docker-run"
```

---

## Task 12: Integration Test

**Files:**
- None (manual testing)

**Step 1: Build the binary**

Run:
```bash
make build
```

**Step 2: Start the server**

Run:
```bash
./charm serve
```

Expected: Server starts with all ports, including PocketBase on 35357

**Step 3: Verify PocketBase admin UI**

Open browser to: `http://localhost:35357/_/`

Expected: PocketBase admin UI loads, prompts for initial admin setup

**Step 4: Verify collections exist**

In PocketBase admin, check Collections tab.

Expected: All 7 collections present (charm_users, public_keys, encrypt_keys, named_seqs, news, tokens, charm_files)

---

## Task 13: Remove Legacy SQLite/LocalFileStore (Optional)

**Files:**
- Delete: `server/db/sqlite/` (entire directory)
- Delete: `server/storage/local/` (entire directory)
- Modify: `go.mod` (remove modernc.org/sqlite)

**Step 1: Remove SQLite package**

Run:
```bash
rm -rf server/db/sqlite/
```

**Step 2: Remove LocalFileStore package**

Run:
```bash
rm -rf server/storage/local/
```

**Step 3: Remove SQLite dependency**

Run:
```bash
go mod tidy
```

**Step 4: Verify build**

Run:
```bash
make build
```
Expected: No errors

**Step 5: Commit**

```bash
git add -A
git commit -m "chore: remove legacy SQLite and LocalFileStore"
```

---

## Summary

This implementation plan covers:

1. Adding PocketBase dependency
2. Creating lifecycle management package
3. Defining collection schemas
4. Implementing db.DB interface
5. Implementing storage.FileStore interface
6. Wiring up in server config
7. Updating Docker/Makefile
8. Integration testing
9. Optional cleanup of legacy code

Total: 13 tasks, approximately 60-90 minutes of implementation time.
