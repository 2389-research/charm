// ABOUTME: Unit tests for HTTP middleware functions.
// ABOUTME: Tests request size limits for standard and filesystem endpoints.
package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRequestLimitMiddleware_NonFSEndpoint_ExceedsLimit tests that non-FS endpoints
// reject requests with Content-Length > 1MB (413 status).
func TestRequestLimitMiddleware_NonFSEndpoint_ExceedsLimit(t *testing.T) {
	middleware := RequestLimitMiddleware()

	// Create a simple handler that would normally succeed
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	wrappedHandler := middleware(handler)

	// Create request with Content-Length > 1MB for non-FS endpoint
	body := bytes.NewReader([]byte("test body"))
	req := httptest.NewRequest("POST", "/v1/api/something", body)
	req.ContentLength = 2 * 1024 * 1024 // 2MB

	rr := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected status %d (413), got %d", http.StatusRequestEntityTooLarge, rr.Code)
	}

	expectedBody := http.StatusText(http.StatusRequestEntityTooLarge) + "\n"
	if rr.Body.String() != expectedBody {
		t.Errorf("expected body %q, got %q", expectedBody, rr.Body.String())
	}
}

// TestRequestLimitMiddleware_FSEndpoint_1MBAllowed tests that FS endpoints
// allow requests with Content-Length > 1MB but < 1GB.
func TestRequestLimitMiddleware_FSEndpoint_1MBAllowed(t *testing.T) {
	middleware := RequestLimitMiddleware()

	// Create a handler that reads and returns success
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to read the body
		_, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("unexpected error reading body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	wrappedHandler := middleware(handler)

	// Create request with Content-Length > 1MB but < 1GB for FS endpoint
	body := bytes.NewReader([]byte("test file content"))
	req := httptest.NewRequest("POST", "/v1/fs/upload", body)
	req.ContentLength = 10 * 1024 * 1024 // 10MB

	rr := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d (200), got %d", http.StatusOK, rr.Code)
	}

	if rr.Body.String() != "success" {
		t.Errorf("expected body %q, got %q", "success", rr.Body.String())
	}
}

// TestRequestLimitMiddleware_FSEndpoint_ExceedsLimit tests that FS endpoints
// reject requests with Content-Length > 1GB (413 status).
func TestRequestLimitMiddleware_FSEndpoint_ExceedsLimit(t *testing.T) {
	middleware := RequestLimitMiddleware()

	// Create a simple handler that would normally succeed
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	wrappedHandler := middleware(handler)

	// Create request with Content-Length > 1GB for FS endpoint
	body := bytes.NewReader([]byte("test body"))
	req := httptest.NewRequest("POST", "/v1/fs/upload", body)
	req.ContentLength = 2 * 1024 * 1024 * 1024 // 2GB

	rr := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected status %d (413), got %d", http.StatusRequestEntityTooLarge, rr.Code)
	}

	expectedBody := http.StatusText(http.StatusRequestEntityTooLarge) + "\n"
	if rr.Body.String() != expectedBody {
		t.Errorf("expected body %q, got %q", expectedBody, rr.Body.String())
	}
}

// TestRequestLimitMiddleware_BodyExceedsLimit_NoContentLength tests that when
// a request body exceeds the limit without Content-Length header, the middleware
// triggers 413 when the handler attempts to read beyond the limit.
func TestRequestLimitMiddleware_BodyExceedsLimit_NoContentLength(t *testing.T) {
	middleware := RequestLimitMiddleware()

	// Create a handler that tries to read the body
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to read more than the limit
		_, err := io.ReadAll(r.Body)
		if err != nil {
			// MaxBytesReader returns an error when limit is exceeded
			if !strings.Contains(err.Error(), "request body too large") &&
				!strings.Contains(err.Error(), "http: request body too large") {
				t.Errorf("expected 'request body too large' error, got: %v", err)
			}
			http.Error(w, "Request Entity Too Large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	wrappedHandler := middleware(handler)

	// Create request with body > 1MB for non-FS endpoint, but no Content-Length
	largeBody := bytes.NewReader(make([]byte, 2*1024*1024)) // 2MB
	req := httptest.NewRequest("POST", "/v1/api/something", largeBody)
	// Don't set ContentLength - let it be -1

	rr := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected status %d (413), got %d", http.StatusRequestEntityTooLarge, rr.Code)
	}
}

// TestRequestLimitMiddleware_NonFSEndpoint_WithinLimit tests that non-FS endpoints
// allow requests with Content-Length <= 1MB.
func TestRequestLimitMiddleware_NonFSEndpoint_WithinLimit(t *testing.T) {
	middleware := RequestLimitMiddleware()

	// Create a handler that reads and returns success
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("unexpected error reading body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	wrappedHandler := middleware(handler)

	// Create request with Content-Length <= 1MB for non-FS endpoint
	body := bytes.NewReader(make([]byte, 512*1024)) // 512KB
	req := httptest.NewRequest("POST", "/v1/api/something", body)
	req.ContentLength = 512 * 1024

	rr := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d (200), got %d", http.StatusOK, rr.Code)
	}
}

// TestRequestLimitMiddleware_FSEndpoint_WithinLimit tests that FS endpoints
// allow requests with Content-Length <= 1GB.
func TestRequestLimitMiddleware_FSEndpoint_WithinLimit(t *testing.T) {
	middleware := RequestLimitMiddleware()

	// Create a handler that reads and returns success
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("unexpected error reading body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	wrappedHandler := middleware(handler)

	// Create request with Content-Length <= 1GB for FS endpoint
	body := bytes.NewReader(make([]byte, 100*1024)) // 100KB (small for performance)
	req := httptest.NewRequest("POST", "/v1/fs/upload", body)
	req.ContentLength = 500 * 1024 * 1024 // 500MB (within 1GB limit)

	rr := httptest.NewRecorder()
	wrappedHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status %d (200), got %d", http.StatusOK, rr.Code)
	}
}

// TestRequestLimitMiddleware_FSEndpoint_PathVariations tests that different
// /v1/fs paths are correctly identified as FS endpoints.
func TestRequestLimitMiddleware_FSEndpoint_PathVariations(t *testing.T) {
	testCases := []struct {
		path         string
		isFSEndpoint bool
	}{
		{"/v1/fs/upload", true},
		{"/v1/fs/download", true},
		{"/v1/fs", true},
		{"/v1/fs/nested/path", true},
		{"/v1/fsomething", true}, // HasPrefix matches this
		{"/v1/api/fs", false},
		{"/v2/fs/upload", false},
	}

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			middleware := RequestLimitMiddleware()

			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			wrappedHandler := middleware(handler)

			// Create request with 10MB body
			body := bytes.NewReader([]byte("test"))
			req := httptest.NewRequest("POST", tc.path, body)
			req.ContentLength = 10 * 1024 * 1024 // 10MB

			rr := httptest.NewRecorder()
			wrappedHandler.ServeHTTP(rr, req)

			if tc.isFSEndpoint {
				// FS endpoints allow 10MB (< 1GB)
				if rr.Code != http.StatusOK {
					t.Errorf("expected FS endpoint to allow 10MB, got status %d", rr.Code)
				}
			} else {
				// Non-FS endpoints reject > 1MB
				if rr.Code != http.StatusRequestEntityTooLarge {
					t.Errorf("expected non-FS endpoint to reject 10MB, got status %d", rr.Code)
				}
			}
		})
	}
}
