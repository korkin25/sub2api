package service

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestAttributionRegistrationValidation(t *testing.T) {
	root := AttributionRegistration{SessionID: "root", ClientKind: "codex", ProjectID: "project", TaskID: "root"}
	require.NoError(t, root.Validate())
	child := AttributionRegistration{SessionID: "child", ClientKind: "codex", ParentSessionID: "root"}
	require.NoError(t, child.Validate())
	bad := root
	bad.ProjectID = ""
	require.Error(t, bad.Validate())
	bad = root
	bad.TaskName = strings.Repeat("я", 301)
	require.Error(t, bad.Validate())
	bad = root
	bad.Host = "bad\nheader"
	require.Error(t, bad.Validate())
	bad = root
	bad.ParentSessionID = bad.SessionID
	require.Error(t, bad.Validate())
}
func TestCaptureUsageClaudeBeforeRewrite(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	metadata, _ := json.Marshal(map[string]string{"device_id": "dev", "session_id": "root-a"})
	body, _ := json.Marshal(map[string]any{"metadata": map[string]string{"user_id": string(metadata)}})
	original := string(body)
	CaptureUsageSessionFromBody(c, body)
	require.Equal(t, "root-a", ExtractClientSessionID(c))
	require.Equal(t, original, string(body))
	CaptureUsageSessionFromBody(c, []byte(`{"metadata":{"user_id":{"session_id":"rewritten"}}}`))
	require.Equal(t, "root-a", ExtractClientSessionID(c))
}
func TestCaptureUsageConcurrentCodexThreads(t *testing.T) {
	var wg sync.WaitGroup
	for _, id := range []string{"project-a-child", "project-b-root"} {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
			raw, _ := json.Marshal(map[string]string{"thread_id": id, "session_id": "shared-parent"})
			c.Request.Header.Set("x-codex-turn-metadata", string(raw))
			c.Request.Header.Set("Session_id", "legacy")
			require.Equal(t, id, ExtractClientSessionID(c))
			CaptureUsageSessionFromBody(c, nil)
			c.Request.Header.Set("x-codex-turn-metadata", `{"thread_id":"upstream-rewrite"}`)
			require.Equal(t, id, ExtractClientSessionID(c))
		}()
	}
	wg.Wait()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	require.Empty(t, ExtractClientSessionID(c))
}

func TestCaptureUsageDesktopAndWebSocketTurns(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
	c.Request.Header.Set("x-codex-turn-metadata", `{"thread_id":"handshake"}`)
	first := []byte(`{"client_metadata":{"x-codex-turn-metadata":"{\"thread_id\":\"first\"}"}}`)
	second := []byte(`{"client_metadata":{"thread_id":"second"}}`)
	require.Equal(t, "first", ExtractUsageSessionFromBody(c, first))
	CaptureUsageSessionFromBody(c, first)
	require.Equal(t, "first", ExtractClientSessionID(c))
	require.Equal(t, "second", ExtractUsageSessionFromBody(c, second))
	require.Equal(t, "handshake", ExtractUsageSessionFromBody(c, nil))
	require.Equal(t, "first", ExtractClientSessionID(c))
}

func TestCaptureUsageUnknownRemainsUnknown(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	CaptureUsageSessionFromBody(c, []byte(`{"model":"m"}`))
	c.Request.Header.Set("session_id", "provider-generated")
	require.Empty(t, ExtractClientSessionID(c))
}
