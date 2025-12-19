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
func (d *DB) pb() core.App {
	return d.app.PB()
}

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
