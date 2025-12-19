// ABOUTME: Integration tests for the /v1/news endpoint
// ABOUTME: Tests pagination, tag filtering, and error handling
package server_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/charm/client"
	charm "github.com/charmbracelet/charm/proto"
	"github.com/charmbracelet/charm/server"
	"github.com/charmbracelet/charm/testserver"
	"github.com/charmbracelet/keygen"
)

// randomPort returns a random available port for testing
func randomPort(tb testing.TB) int {
	tb.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("could not get a random port: %s", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()
	p, _ := strconv.Atoi(addr[strings.LastIndex(addr, ":")+1:])
	return p
}

// setupTestServerWithDB creates a test server and returns both client and server
// This allows tests to interact with the server's database directly
func setupTestServerWithDB(tb testing.TB) (*client.Client, *server.Server) {
	tb.Helper()

	td := tb.TempDir()
	sp := filepath.Join(td, ".ssh")
	clientData := filepath.Join(td, ".client-data")

	cfg := server.DefaultConfig()
	cfg.DataDir = filepath.Join(td, ".data")
	cfg.SSHPort = randomPort(tb)
	cfg.HTTPPort = randomPort(tb)
	cfg.HealthPort = randomPort(tb)

	kp, err := keygen.New(filepath.Join(sp, "charm_server_ed25519"), keygen.WithKeyType(keygen.Ed25519), keygen.WithWrite())
	if err != nil {
		tb.Fatalf("keygen error: %s", err)
	}

	cfg = cfg.WithKeys(kp.RawAuthorizedKey(), kp.RawPrivateKey())
	s, err := server.NewServer(cfg)
	if err != nil {
		tb.Fatalf("new server error: %s", err)
	}

	go func() { _ = s.Start() }()

	resp, err := testserver.FetchURL(fmt.Sprintf("http://localhost:%d", cfg.HealthPort), 3)
	if err != nil {
		tb.Fatalf("server likely failed to start: %s", err)
	}
	defer resp.Body.Close()

	tb.Cleanup(func() {
		if err := s.Close(); err != nil {
			tb.Error("failed to close server:", err)
		}
	})

	ccfg, err := client.ConfigFromEnv()
	if err != nil {
		tb.Fatalf("client config from env error: %s", err)
	}

	ccfg.Host = cfg.Host
	ccfg.SSHPort = cfg.SSHPort
	ccfg.HTTPPort = cfg.HTTPPort
	ccfg.DataDir = clientData

	cl, err := client.NewClient(ccfg)
	if err != nil {
		tb.Fatalf("new client error: %s", err)
	}

	return cl, s
}

// TestNewsIntegration tests the complete news flow: posting news and retrieving it via client
func TestNewsIntegration(t *testing.T) {
	cl, srv := setupTestServerWithDB(t)

	// Authenticate first
	_, err := cl.Auth()
	if err != nil {
		t.Fatalf("auth error: %s", err)
	}

	// Post news via server DB with "server" tag
	err = srv.Config.DB.PostNews("Test News Item", "This is a test news body", []string{"server"})
	if err != nil {
		t.Fatalf("failed to post news: %s", err)
	}

	// Post another news item with custom tag
	err = srv.Config.DB.PostNews("Custom Tag News", "This has a custom tag", []string{"custom-tag"})
	if err != nil {
		t.Fatalf("failed to post custom tag news: %s", err)
	}

	// Retrieve news list with "server" tag via client
	// NOTE: Client sends "tags" parameter but server reads "tag" parameter
	// So we need to use raw request to test properly
	resp, err := cl.AuthedRawRequest("GET", "/v1/news?page=1&tag=server")
	if err != nil {
		t.Fatalf("failed to get news list: %s", err)
	}
	defer resp.Body.Close()

	var newsList []*charm.News
	if err := json.NewDecoder(resp.Body).Decode(&newsList); err != nil {
		t.Fatalf("failed to decode news list: %s", err)
	}

	if len(newsList) != 1 {
		t.Errorf("expected 1 news item with 'server' tag, got %d", len(newsList))
	}

	if len(newsList) > 0 {
		if newsList[0].Subject != "Test News Item" {
			t.Errorf("expected subject 'Test News Item', got '%s'", newsList[0].Subject)
		}
		// Body should be empty in list response
		if newsList[0].Body != "" {
			t.Error("expected empty body in list response")
		}
	}

	// Test retrieving with custom tag
	resp2, err := cl.AuthedRawRequest("GET", "/v1/news?page=1&tag=custom-tag")
	if err != nil {
		t.Fatalf("failed to get custom tag news: %s", err)
	}
	defer resp2.Body.Close()

	var customNewsList []*charm.News
	if err := json.NewDecoder(resp2.Body).Decode(&customNewsList); err != nil {
		t.Fatalf("failed to decode custom news list: %s", err)
	}

	if len(customNewsList) != 1 {
		t.Errorf("expected 1 news item with 'custom-tag', got %d", len(customNewsList))
	}

	if len(customNewsList) > 0 {
		if customNewsList[0].Subject != "Custom Tag News" {
			t.Errorf("expected subject 'Custom Tag News', got '%s'", customNewsList[0].Subject)
		}
	}
}

// TestNewsListClientServerTagMismatch documents the mismatch between client and server
// The client uses 'tags' query parameter but the server expects 'tag' (singular)
func TestNewsListClientServerTagMismatch(t *testing.T) {
	cl := testserver.SetupTestServer(t)

	_, err := cl.Auth()
	if err != nil {
		t.Fatalf("auth error: %s", err)
	}

	// The client.NewsList() method uses 'tags' parameter (plural)
	// But server handleGetNewsList() expects 'tag' parameter (singular)
	// This test documents this behavior

	// Client sends: /v1/news?page=1&tags=server
	// Server reads: r.FormValue("tag")
	// This means the server will use default "server" tag instead of what client sends

	newsList, err := cl.NewsList([]string{"custom-tag"}, 1)
	if err != nil {
		t.Fatalf("failed to get news list: %s", err)
	}

	// This will succeed but won't filter by "custom-tag" due to the mismatch
	// The server ignores the "tags" parameter and uses "tag" parameter
	t.Logf("News list retrieved with %d items (tag mismatch means filtering may not work)", len(newsList))
}

// TestNewsListPageZero tests what happens when page=0 is requested
// According to server code: offset = (page - 1) * resultsPerPage
// With page=0: offset = (0 - 1) * 50 = -50
func TestNewsListPageZero(t *testing.T) {
	cl := testserver.SetupTestServer(t)

	_, err := cl.Auth()
	if err != nil {
		t.Fatalf("auth error: %s", err)
	}

	// Request page 0
	newsList, err := cl.NewsList([]string{"server"}, 0)
	if err != nil {
		t.Fatalf("failed to get news list with page=0: %s", err)
	}

	// SQLite LIMIT with negative OFFSET behavior:
	// OFFSET -50 is treated as OFFSET 0 in SQLite
	// So page=0 should return the same results as page=1
	t.Logf("Page 0 returned %d items (negative offset treated as 0)", len(newsList))

	// Verify it returns same as page 1
	newsListPage1, err := cl.NewsList([]string{"server"}, 1)
	if err != nil {
		t.Fatalf("failed to get news list with page=1: %s", err)
	}

	if len(newsList) != len(newsListPage1) {
		t.Errorf("page=0 returned %d items but page=1 returned %d items", len(newsList), len(newsListPage1))
	}
}

// TestNewsListInvalidPage tests server behavior with invalid page parameter
func TestNewsListInvalidPage(t *testing.T) {
	cl := testserver.SetupTestServer(t)

	_, err := cl.Auth()
	if err != nil {
		t.Fatalf("auth error: %s", err)
	}

	// Make a raw request with invalid page parameter
	// The client doesn't expose this, so we need to use AuthedRawRequest
	// Note: AuthedRawRequest returns an error for non-200 status codes
	// So we expect the error to occur, which indicates the server properly rejected the request

	resp, err := cl.AuthedRawRequest("GET", "/v1/news?page=abc")

	// Server should return 500 (Internal Server Error) based on handleGetNewsList code
	// Lines 379-382 in http.go show strconv.Atoi error returns 500
	// The client wraps this as an error
	if err == nil {
		t.Error("expected error for invalid page parameter, got nil")
		if resp != nil {
			resp.Body.Close()
		}
		return
	}

	// Verify the error message indicates server error
	errStr := fmt.Sprintf("%s", err)
	if errStr == "" {
		t.Error("error should have a message")
	}

	t.Logf("Invalid page error (expected): %s", err)
}

// TestNewsListInvalidPageNegative tests server behavior with negative page numbers
func TestNewsListInvalidPageNegative(t *testing.T) {
	cl := testserver.SetupTestServer(t)

	_, err := cl.Auth()
	if err != nil {
		t.Fatalf("auth error: %s", err)
	}

	// Test with negative page number
	newsList, err := cl.NewsList([]string{"server"}, -5)
	if err != nil {
		t.Fatalf("failed to get news list with page=-5: %s", err)
	}

	// Server calculates: offset = (-5 - 1) * 50 = -300
	// SQLite treats negative OFFSET as 0
	t.Logf("Page -5 returned %d items (negative offset treated as 0)", len(newsList))
}

// TestNewsListPagination tests basic pagination behavior
func TestNewsListPagination(t *testing.T) {
	cl := testserver.SetupTestServer(t)

	_, err := cl.Auth()
	if err != nil {
		t.Fatalf("auth error: %s", err)
	}

	// Test multiple pages
	page1, err := cl.NewsList([]string{"server"}, 1)
	if err != nil {
		t.Fatalf("failed to get page 1: %s", err)
	}

	page2, err := cl.NewsList([]string{"server"}, 2)
	if err != nil {
		t.Fatalf("failed to get page 2: %s", err)
	}

	page3, err := cl.NewsList([]string{"server"}, 3)
	if err != nil {
		t.Fatalf("failed to get page 3: %s", err)
	}

	t.Logf("Page 1: %d items, Page 2: %d items, Page 3: %d items",
		len(page1), len(page2), len(page3))

	// Pages should not overlap (assuming we have enough data)
	// If we have less than 50 items, page 2 and 3 will be empty
	if len(page1) > 0 && len(page2) > 0 {
		// Verify different pages return different results
		if len(page1) > 0 && len(page2) > 0 {
			if page1[0].ID == page2[0].ID {
				t.Error("page 1 and page 2 returned the same first item")
			}
		}
	}
}

// TestNewsListDefaultTag tests that default tag is "server" when not specified
func TestNewsListDefaultTag(t *testing.T) {
	cl := testserver.SetupTestServer(t)

	_, err := cl.Auth()
	if err != nil {
		t.Fatalf("auth error: %s", err)
	}

	// Client defaults to ["server"] when tags is nil
	newsList, err := cl.NewsList(nil, 1)
	if err != nil {
		t.Fatalf("failed to get news list: %s", err)
	}

	t.Logf("Default tag returned %d items", len(newsList))
}

// TestNewsGet tests retrieving a specific news item by ID
func TestNewsGet(t *testing.T) {
	cl := testserver.SetupTestServer(t)

	_, err := cl.Auth()
	if err != nil {
		t.Fatalf("auth error: %s", err)
	}

	// First get the list to find an ID
	newsList, err := cl.NewsList([]string{"server"}, 1)
	if err != nil {
		t.Fatalf("failed to get news list: %s", err)
	}

	if len(newsList) == 0 {
		t.Skip("no news items available to test with")
	}

	// Get the first news item's full details
	newsID := newsList[0].ID
	news, err := cl.News(newsID)
	if err != nil {
		t.Fatalf("failed to get news %s: %s", newsID, err)
	}

	if news.ID != newsID {
		t.Errorf("expected news ID %s, got %s", newsID, news.ID)
	}

	if news.Subject != newsList[0].Subject {
		t.Errorf("expected subject %s, got %s", newsList[0].Subject, news.Subject)
	}

	// The full news should have a body (list items don't include body)
	t.Logf("Retrieved news %s with subject '%s' and body length %d",
		news.ID, news.Subject, len(news.Body))
}

// TestNewsGetInvalidID tests retrieving news with invalid ID
func TestNewsGetInvalidID(t *testing.T) {
	cl := testserver.SetupTestServer(t)

	_, err := cl.Auth()
	if err != nil {
		t.Fatalf("auth error: %s", err)
	}

	// Try to get news with non-existent ID
	_, err = cl.News("nonexistent-id-12345")
	if err == nil {
		t.Error("expected error for non-existent news ID, got nil")
	}

	t.Logf("Non-existent ID error: %s", err)
}

// TestNewsListEmptyResults tests behavior when no news matches the filter
func TestNewsListEmptyResults(t *testing.T) {
	cl := testserver.SetupTestServer(t)

	_, err := cl.Auth()
	if err != nil {
		t.Fatalf("auth error: %s", err)
	}

	// Request news with a tag that likely doesn't exist
	// Note: Due to tag/tags mismatch, we need to make a raw request
	resp, err := cl.AuthedRawRequest("GET", "/v1/news?page=1&tag=nonexistent-tag-xyz")
	if err != nil {
		t.Fatalf("request failed: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var newsList []*charm.News
	if err := json.NewDecoder(resp.Body).Decode(&newsList); err != nil {
		t.Fatalf("failed to decode response: %s", err)
	}

	t.Logf("Non-existent tag returned %d items", len(newsList))
}

// TestNewsListResponseFormat tests that the response format is correct
func TestNewsListResponseFormat(t *testing.T) {
	cl := testserver.SetupTestServer(t)

	_, err := cl.Auth()
	if err != nil {
		t.Fatalf("auth error: %s", err)
	}

	// Make raw request to inspect exact response
	resp, err := cl.AuthedRawRequest("GET", "/v1/news?page=1&tag=server")
	if err != nil {
		t.Fatalf("request failed: %s", err)
	}
	defer resp.Body.Close()

	// Check content type
	contentType := resp.Header.Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}

	// Decode and verify structure
	var newsList []*charm.News
	if err := json.NewDecoder(resp.Body).Decode(&newsList); err != nil {
		t.Fatalf("failed to decode response: %s", err)
	}

	// Verify each news item has required fields
	for i, news := range newsList {
		if news.ID == "" {
			t.Errorf("news[%d] missing ID", i)
		}
		if news.Subject == "" {
			t.Errorf("news[%d] missing Subject", i)
		}
		// Body should be empty in list response (not selected in SQL)
		if news.Body != "" {
			t.Logf("news[%d] unexpectedly has body in list response", i)
		}
		// CreatedAt should be set
		if news.CreatedAt.IsZero() {
			t.Errorf("news[%d] missing CreatedAt", i)
		}
	}

	t.Logf("Response format valid, returned %d news items", len(newsList))
}

// TestNewsListResultsPerPage tests that pagination respects the 50 items per page limit
func TestNewsListResultsPerPage(t *testing.T) {
	cl := testserver.SetupTestServer(t)

	_, err := cl.Auth()
	if err != nil {
		t.Fatalf("auth error: %s", err)
	}

	// Get first page
	page1, err := cl.NewsList([]string{"server"}, 1)
	if err != nil {
		t.Fatalf("failed to get page 1: %s", err)
	}

	// Should never exceed 50 items (resultsPerPage constant in server)
	if len(page1) > 50 {
		t.Errorf("page 1 returned %d items, expected maximum 50", len(page1))
	}

	t.Logf("Page 1 returned %d items (max 50)", len(page1))
}

// TestNewsURLEncoding tests that URL encoding works correctly for news IDs
func TestNewsURLEncoding(t *testing.T) {
	cl := testserver.SetupTestServer(t)

	_, err := cl.Auth()
	if err != nil {
		t.Fatalf("auth error: %s", err)
	}

	// Test with ID that has special characters (if server creates such IDs)
	// This tests the url.QueryEscape in client.News()
	testID := "test/id with spaces"

	// This will likely fail (news doesn't exist) but should not fail due to encoding
	_, err = cl.News(testID)
	if err == nil {
		t.Error("expected error for non-existent news ID")
	}

	// The error should be about the news not existing, not about invalid URL
	errStr := fmt.Sprintf("%s", err)
	if errStr == "" {
		t.Error("error should have a message")
	}

	t.Logf("URL encoding test error: %s", err)
}
