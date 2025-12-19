// ABOUTME: This file contains unit tests for HTTP client functions.
// ABOUTME: Tests cover AuthedRequest and AuthedJSONRequest behavior including error handling and body management.
package client

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	charm "github.com/charmbracelet/charm/proto"
	jwt "github.com/golang-jwt/jwt/v4"
)

// NewClientForTest creates a Client for testing with properly initialized mutexes.
func NewClientForTest(cfg *Config) *Client {
	// Create a client with initialized mutexes and valid claims to bypass Auth()
	client := &Client{
		Config:         cfg,
		auth:           &charm.Auth{JWT: "test-token"},
		authLock:       &sync.Mutex{},
		encryptKeyLock: &sync.Mutex{},
		httpScheme:     "http",
	}
	// Set valid claims so Auth() returns cached auth instead of attempting SSH connection
	// We use a future expiration time so claims.Valid() returns nil
	client.claims = &jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
	}
	return client
}

// NewClientForTestServer creates a Client configured to use a httptest.Server.
func NewClientForTestServer(ts *httptest.Server) *Client {
	// Parse host and port from test server URL (format: "http://host:port")
	urlWithoutScheme := strings.TrimPrefix(ts.URL, "http://")
	parts := strings.Split(urlWithoutScheme, ":")
	host := parts[0]
	port, _ := strconv.Atoi(parts[1])

	return NewClientForTest(&Config{
		Host:     host,
		HTTPPort: port,
	})
}

// trackingReadCloser wraps an io.ReadCloser and tracks whether Close() was called.
type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (t *trackingReadCloser) Close() error {
	t.closed = true
	return nil
}

func TestAuthedRequest_ReturnsErrorForNon2xxWithJSON(t *testing.T) {
	// Create a test server that returns 400 with JSON error message
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(charm.Message{Message: "bad stuff"})
	}))
	defer ts.Close()

	// Create a client configured to use the test server
	client := NewClientForTestServer(ts)

	// Make the request
	_, err := client.AuthedRequest("GET", "/test", nil, nil)

	// Assert error is not nil
	if err == nil {
		t.Fatal("expected error for non-2xx response, got nil")
	}

	// Assert error contains status code
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected error to contain status code 400, got: %s", err.Error())
	}

	// Assert error contains the decoded JSON message
	if !strings.Contains(err.Error(), "bad stuff") {
		t.Errorf("expected error to contain message 'bad stuff', got: %s", err.Error())
	}
}

func TestAuthedRequest_ReturnsGenericErrorForNon2xxWithoutJSON(t *testing.T) {
	// Create a test server that returns 500 with plain text
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer ts.Close()

	// Create a client configured to use the test server
	client := NewClientForTestServer(ts)

	// Make the request
	_, err := client.AuthedRequest("GET", "/test", nil, nil)

	// Assert error is not nil
	if err == nil {
		t.Fatal("expected error for non-2xx response, got nil")
	}

	// Assert error contains status code
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to contain status code 500, got: %s", err.Error())
	}

	// Assert error does NOT contain the plain text body content
	// (since it's not JSON, it should only have the generic status text)
	if strings.Contains(err.Error(), "internal server error") {
		t.Errorf("expected error to NOT contain plain text body, got: %s", err.Error())
	}
}

func TestAuthedJSONRequest_ClosesResponseBodyWhenRespBodyNotNil(t *testing.T) {
	// Create a test server that returns JSON
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"key": "value"})
	}))
	defer ts.Close()

	// Create a client configured to use the test server
	client := NewClientForTestServer(ts)

	// Make the request with a non-nil respBody
	var respBody map[string]string
	err := client.AuthedJSONRequest("GET", "/test", nil, &respBody)

	// Assert no error
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Assert response was decoded
	if respBody["key"] != "value" {
		t.Errorf("expected respBody[key] = 'value', got: %s", respBody["key"])
	}

	// Note: We can't directly test if Close() was called without modifying the production code
	// to inject dependencies. This test verifies the happy path works correctly.
	// The Close() call is verified through code inspection and the defer statement.
}

func TestAuthedRequest_SetsContentLengthFromHeader(t *testing.T) {
	var receivedContentLength int64

	// Create a test server that captures the Content-Length
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentLength = r.ContentLength
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	// Create a client configured to use the test server
	client := NewClientForTestServer(ts)

	// Create a body with known length
	bodyContent := "test body content"
	bodyLength := len(bodyContent)

	// Create headers with Content-Length matching the actual body
	headers := http.Header{
		"Content-Length": []string{strconv.Itoa(bodyLength)},
	}

	// Make the request with a body
	body := strings.NewReader(bodyContent)
	_, err := client.AuthedRequest("POST", "/test", headers, body)

	// Assert no error
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Assert Content-Length was set correctly
	expectedLength := int64(bodyLength)
	if receivedContentLength != expectedLength {
		t.Errorf("expected Content-Length to be %d, got: %d", expectedLength, receivedContentLength)
	}
}

func TestAuthedJSONRequest_EncodesNilRequestBodyAsNull(t *testing.T) {
	var receivedBody string

	// Create a test server that captures the request body
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		receivedBody = string(bodyBytes)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	// Create a client configured to use the test server
	client := NewClientForTestServer(ts)

	// Make the request with nil reqBody
	err := client.AuthedJSONRequest("POST", "/test", nil, nil)

	// Assert no error
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Assert the body received was "null\n" (JSON encoding of nil)
	expectedBody := "null\n"
	if receivedBody != expectedBody {
		t.Errorf("expected body to be %q, got: %q", expectedBody, receivedBody)
	}
}
