// ABOUTME: Test helpers for the client package, providing utilities for testing in other packages.
// ABOUTME: This file contains functions that should only be used in test code.
//go:build !production

package client

import (
	"sync"
	"time"

	charm "github.com/charmbracelet/charm/proto"
	jwt "github.com/golang-jwt/jwt/v4"
)

// NewTestClientWithKeys creates a minimal client for testing purposes with the provided encryption keys.
// This function is exported only for testing and should not be used in production code.
// It creates a client that can return encryption keys via EncryptKeys() but may not have
// full authentication or configuration setup.
func NewTestClientWithKeys(keys []*charm.EncryptKey) *Client {
	// Create valid JWT claims to avoid Auth() trying to establish SSH connection
	now := time.Now()
	claims := &jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		Issuer:    "test",
		Subject:   "test-user",
		ID:        "test-id",
	}

	// Create auth object with empty encrypt keys
	// (the plainTextEncryptKeys will be used instead)
	auth := &charm.Auth{
		JWT:         "test-jwt",
		ID:          "test-id",
		HTTPScheme:  "http",
		PublicKey:   "test-public-key",
		EncryptKeys: []*charm.EncryptKey{}, // Empty, will use plainTextEncryptKeys
	}

	return &Client{
		Config: &Config{
			Host: "localhost",
		},
		auth:                 auth,
		claims:               claims,
		authLock:             &sync.Mutex{},
		encryptKeyLock:       &sync.Mutex{},
		plainTextEncryptKeys: keys,
	}
}
