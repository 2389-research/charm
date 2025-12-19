# client_test.go - DISABLED

This test file has been temporarily disabled during the SQLite migration.

The tests in this file were testing internal Badger-specific functions:
- `encryptKeyToBadgerKey()` - converted encryption keys for Badger
- `openDB()` - opened Badger databases with encryption

These functions have been removed as part of the SQLite migration.

## Next Steps

These tests need to be rewritten in Task 8 (Update Sync Logic) to test the new SQLite-based implementation. The new tests should verify:

1. Database opening with proper encryption at the SQLite level
2. Sync operations (backup/restore) working correctly
3. Integration tests for the complete sync flow

The disabled test file is at: `kv/client_test.go.disabled`
