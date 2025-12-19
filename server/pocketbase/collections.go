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

	// Look up the charm_users collection to get its actual ID
	usersCol, err := a.pb.FindCollectionByNameOrId(CollectionCharmUsers)
	if err != nil {
		return err
	}

	collection := core.NewBaseCollection(CollectionPublicKeys)
	collection.Fields.Add(
		&core.RelationField{
			Name:          "user",
			CollectionId:  usersCol.Id,
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

	// Look up the public_keys collection to get its actual ID
	keysCol, err := a.pb.FindCollectionByNameOrId(CollectionPublicKeys)
	if err != nil {
		return err
	}

	collection := core.NewBaseCollection(CollectionEncryptKeys)
	collection.Fields.Add(
		&core.RelationField{
			Name:          "public_key",
			CollectionId:  keysCol.Id,
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

	// Look up the charm_users collection to get its actual ID
	usersCol, err := a.pb.FindCollectionByNameOrId(CollectionCharmUsers)
	if err != nil {
		return err
	}

	collection := core.NewBaseCollection(CollectionNamedSeqs)
	collection.Fields.Add(
		&core.RelationField{
			Name:          "user",
			CollectionId:  usersCol.Id,
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
