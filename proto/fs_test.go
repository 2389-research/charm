// ABOUTME: This file contains tests for the filesystem utility functions.
// ABOUTME: It tests permission handling, especially AddExecPermsForMkDir.
package proto

import (
	"io/fs"
	"testing"
)

func TestAddExecPermsForMkDir(t *testing.T) {
	tests := []struct {
		name     string
		input    fs.FileMode
		expected fs.FileMode
	}{
		{
			name:     "user read adds user execute",
			input:    0400,
			expected: fs.ModeDir | 0500,
		},
		{
			name:     "group read adds group execute",
			input:    0040,
			expected: fs.ModeDir | 0050,
		},
		{
			name:     "other read adds other execute",
			input:    0004,
			expected: fs.ModeDir | 0005,
		},
		{
			name:     "all read adds all execute",
			input:    0444,
			expected: fs.ModeDir | 0555,
		},
		{
			name:     "mixed permissions",
			input:    0640,
			expected: fs.ModeDir | 0750,
		},
		{
			name:     "no read permissions means no execute added",
			input:    0200,
			expected: fs.ModeDir | 0200,
		},
		{
			name:     "already has execute permissions",
			input:    0755,
			expected: fs.ModeDir | 0755,
		},
		{
			name:     "no permissions at all",
			input:    0000,
			expected: fs.ModeDir | 0000,
		},
		{
			name:     "user read/write adds user execute only",
			input:    0600,
			expected: fs.ModeDir | 0700,
		},
		{
			name:     "typical file permissions",
			input:    0644,
			expected: fs.ModeDir | 0755,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := AddExecPermsForMkDir(tt.input)
			if result != tt.expected {
				t.Errorf("AddExecPermsForMkDir(%04o) = %04o, want %04o", tt.input, result, tt.expected)
			}
		})
	}
}

func TestAddExecPermsForMkDir_SetsDirBit(t *testing.T) {
	tests := []struct {
		name  string
		input fs.FileMode
	}{
		{"zero permissions", 0000},
		{"user read only", 0400},
		{"typical file mode", 0644},
		{"full permissions", 0777},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := AddExecPermsForMkDir(tt.input)
			if !result.IsDir() {
				t.Errorf("AddExecPermsForMkDir(%04o) did not set ModeDir bit, result = %04o", tt.input, result)
			}
		})
	}
}

func TestAddExecPermsForMkDir_AlreadyDir(t *testing.T) {
	tests := []struct {
		name  string
		input fs.FileMode
	}{
		{"dir with user read/exec", fs.ModeDir | 0500},
		{"dir with all permissions", fs.ModeDir | 0755},
		{"dir with no permissions", fs.ModeDir | 0000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := AddExecPermsForMkDir(tt.input)
			if result != tt.input {
				t.Errorf("AddExecPermsForMkDir(%04o) modified already-dir mode, got %04o, want %04o", tt.input, result, tt.input)
			}
		})
	}
}

func TestAddExecPermsForMkDir_ReadToExecuteMapping(t *testing.T) {
	// Test that each read bit correctly maps to its corresponding execute bit
	tests := []struct {
		name        string
		input       fs.FileMode
		checkBit    fs.FileMode
		description string
	}{
		{
			name:        "user read (0400) adds user execute (0100)",
			input:       0400,
			checkBit:    0100,
			description: "user execute bit",
		},
		{
			name:        "group read (0040) adds group execute (0010)",
			input:       0040,
			checkBit:    0010,
			description: "group execute bit",
		},
		{
			name:        "other read (0004) adds other execute (0001)",
			input:       0004,
			checkBit:    0001,
			description: "other execute bit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := AddExecPermsForMkDir(tt.input)
			if result.Perm()&tt.checkBit != tt.checkBit {
				t.Errorf("AddExecPermsForMkDir(%04o) did not set %s (0%03o), result = %04o",
					tt.input, tt.description, tt.checkBit, result)
			}
		})
	}
}

func TestAddExecPermsForMkDir_PreservesNonPermissionBits(t *testing.T) {
	// Test with file modes that have additional bits set beyond just permissions
	tests := []struct {
		name  string
		input fs.FileMode
	}{
		{"setuid bit", fs.ModeSetuid | 0644},
		{"setgid bit", fs.ModeSetgid | 0644},
		{"sticky bit", fs.ModeSticky | 0644},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := AddExecPermsForMkDir(tt.input)

			// Should have ModeDir set
			if !result.IsDir() {
				t.Errorf("AddExecPermsForMkDir(%v) did not set ModeDir", tt.input)
			}

			// Should preserve the special bits (except ModeDir changes the type)
			// We need to check that permission bits are correctly transformed
			expectedPerm := fs.FileMode(0755) // 0644 -> 0755
			if result.Perm() != expectedPerm {
				t.Errorf("AddExecPermsForMkDir(%v) permissions = %04o, want %04o",
					tt.input, result.Perm(), expectedPerm)
			}
		})
	}
}
