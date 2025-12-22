// Package crypt provides encryption writer/readers.
package crypt

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/charmbracelet/charm/client"
	charm "github.com/charmbracelet/charm/proto"
	"github.com/jacobsa/crypto/siv"
	"github.com/muesli/sasquatch"
)

// ErrIncorrectEncryptKeys is returned when the encrypt keys are missing or
// incorrect for the encrypted data.
var ErrIncorrectEncryptKeys = fmt.Errorf("incorrect or missing encrypt keys")

// Crypt manages the account and encryption keys used for encrypting and
// decrypting.
type Crypt struct {
	keys []*charm.EncryptKey
}

// EncryptedWriter is an io.WriteCloser. All data written to this writer is
// encrypted before being written to the underlying io.Writer.
type EncryptedWriter struct {
	w io.WriteCloser
}

// DecryptedReader is an io.Reader that decrypts data from an encrypted
// underlying io.Reader.
type DecryptedReader struct {
	r io.Reader
}

// NewCrypt authenticates a user to the Charm Cloud and returns a Crypt struct
// ready for encrypting and decrypting.
func NewCrypt() (*Crypt, error) {
	cc, err := client.NewClientWithDefaults()
	if err != nil {
		return nil, err
	}
	eks, err := cc.EncryptKeys()
	if err != nil {
		return nil, err
	}
	if len(eks) == 0 {
		return nil, ErrIncorrectEncryptKeys
	}
	return &Crypt{keys: eks}, nil
}

// NewDecryptedReader creates a new Reader that will read from and decrypt the
// passed in io.Reader of encrypted data.
func (cr *Crypt) NewDecryptedReader(r io.Reader) (*DecryptedReader, error) {
	var sdr io.Reader
	dr := &DecryptedReader{}
	for _, k := range cr.keys {
		id, err := sasquatch.NewScryptIdentity(k.Key)
		if err != nil {
			return nil, err
		}
		sdr, err = sasquatch.Decrypt(r, id)
		if err == nil {
			break
		}
	}
	if sdr == nil {
		return nil, ErrIncorrectEncryptKeys
	}
	dr.r = sdr
	return dr, nil
}

// NewEncryptedWriter creates a new Writer that encrypts all data and writes
// the encrypted data to the supplied io.Writer.
func (cr *Crypt) NewEncryptedWriter(w io.Writer) (*EncryptedWriter, error) {
	ew := &EncryptedWriter{}
	rec, err := sasquatch.NewScryptRecipient(cr.keys[0].Key)
	if err != nil {
		return ew, err
	}
	sew, err := sasquatch.Encrypt(w, rec)
	if err != nil {
		return ew, err
	}
	ew.w = sew
	return ew, nil
}

// Keys returns the EncryptKeys this Crypt is using.
func (cr *Crypt) Keys() []*charm.EncryptKey {
	return cr.keys
}

// EncryptLookupField will deterministically encrypt a string and the same
// encrypted value every time this string is encrypted with the same
// EncryptKey. This is useful if you need to look up an encrypted value without
// knowing the plaintext on the storage side. For writing encrypted data, use
// EncryptedWriter which is non-deterministic.
func (cr *Crypt) EncryptLookupField(field string) (string, error) {
	if field == "" {
		return "", nil
	}
	keyBytes, err := decodeKey(cr.keys[0].Key)
	if err != nil {
		return "", fmt.Errorf("failed to decode encryption key: %w", err)
	}
	ct, err := siv.Encrypt(nil, keyBytes[:32], []byte(field), nil)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(ct), nil
}

// DecryptLookupField decrypts a string encrypted with EncryptLookupField.
func (cr *Crypt) DecryptLookupField(field string) (string, error) {
	if field == "" {
		return "", nil
	}
	ct, err := hex.DecodeString(field)
	if err != nil {
		return "", err
	}
	var pt []byte
	for _, k := range cr.keys {
		keyBytes, err := decodeKey(k.Key)
		if err != nil {
			continue
		}
		pt, err = siv.Decrypt(keyBytes[:32], ct, nil)
		if err == nil {
			break
		}
	}
	if len(pt) == 0 {
		return "", ErrIncorrectEncryptKeys
	}
	return string(pt), nil
}

// Read decrypts and reads data from the underlying io.Reader.
func (dr *DecryptedReader) Read(p []byte) (int, error) {
	return dr.r.Read(p)
}

// Write encrypts data and writes it to the underlying io.WriteCloser.
func (ew *EncryptedWriter) Write(p []byte) (int, error) {
	return ew.w.Write(p)
}

// Close closes the underlying io.WriteCloser.
func (ew *EncryptedWriter) Close() error {
	return ew.w.Close()
}

// decodeKey decodes a key string that may be either base64 or hex encoded.
// It tries base64 first (the current format), then falls back to hex (for test compatibility).
func decodeKey(key string) ([]byte, error) {
	// Try base64 first (production format from client/crypt.go)
	keyBytes, err := base64.StdEncoding.DecodeString(key)
	if err == nil && len(keyBytes) >= 32 {
		return keyBytes, nil
	}

	// Fall back to hex (test format from crypt_test.go)
	keyBytes, err = hex.DecodeString(key)
	if err == nil && len(keyBytes) >= 32 {
		return keyBytes, nil
	}

	return nil, fmt.Errorf("key must be at least 32 bytes when decoded")
}
