package service

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// maxPersistedSessionIDLength bounds the persisted client session identifier to the
// usage_logs.session_id column width (VARCHAR(255)). Longer values are rejected so
// distinct identifiers can never alias through truncation.
const maxPersistedSessionIDLength = 255

// clientSessionIDHeaders extends the OpenAI-compatible sticky-session signals with
// native protocol identifiers that are safe to persist but must not alter OpenAI
// scheduling behavior.
var clientSessionIDHeaders = append(
	append([]string(nil), explicitOpenAIHeaderSessionNames...),
	claudeCodeSessionHeader,
)

// ClaudeCodeSessionIDFromHeader returns the stable Claude Code conversation
// identifier carried by X-Claude-Code-Session-Id. It is intentionally exposed
// separately from ExtractClientSessionID: callers that use it for routing must
// make that scope explicit rather than accidentally changing every protocol's
// session semantics.
func ClaudeCodeSessionIDFromHeader(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	return sanitizeSessionID(c.GetHeader(claudeCodeSessionHeader))
}

// ExtractClientSessionID resolves the explicit client-provided session identifier from
// request headers for usage-log correlation and returns it sanitized. It is
// protocol-agnostic and shared by every gateway handler so all supported protocols
// record session_id through one seam. Returns "" when no valid identifier is present.
//
// This value feeds only usage_logs.session_id persistence. It does NOT affect sticky
// routing, account selection, request_id semantics, or upstream prompt caching, which
// keep their own (intentionally broader) session-signal resolution.
func ExtractClientSessionID(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	if value, ok := c.Get("usage_attribution_session_id"); ok {
		if id, ok := value.(string); ok {
			return id
		}
	}
	return extractUsageSessionHeaders(c)
}

func extractUsageSessionHeaders(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	if id := sanitizeSessionID(c.GetHeader("thread-id")); id != "" {
		return id
	}
	var turn struct {
		ThreadID  string `json:"thread_id"`
		SessionID string `json:"session_id"`
	}
	if raw := c.GetHeader("x-codex-turn-metadata"); len(raw) <= 16384 && json.Unmarshal([]byte(raw), &turn) == nil {
		if id := sanitizeSessionID(turn.ThreadID); id != "" {
			return id
		}
		if id := sanitizeSessionID(turn.SessionID); id != "" {
			return id
		}
	}
	for _, header := range clientSessionIDHeaders {
		if sessionID := sanitizeSessionID(c.GetHeader(header)); sessionID != "" {
			return sessionID
		}
	}
	if isGrokRequestContext(c) {
		if sessionID := sanitizeSessionID(c.GetHeader(grokConversationIDHeader)); sessionID != "" {
			return sessionID
		}
	}
	return ""
}

// sanitizeSessionID normalizes a raw client-supplied session identifier for safe
// persistence: it trims surrounding whitespace, rejects the value outright if it
// contains any control character (CR/LF/tab/NUL/…) so a log- or header-injection style
// payload cannot slip into stored correlation data, and rejects values longer than
// the DB column bound. Absent or invalid input yields "".
func sanitizeSessionID(raw string) string {
	if !utf8.ValidString(raw) {
		return ""
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	count := 0
	for _, r := range trimmed {
		if r < 0x20 || r == 0x7f {
			// An explicit correlation id never legitimately contains control
			// characters; drop the whole value rather than persist a mangled or
			// partially-injected identifier.
			return ""
		}
		count++
		if count > maxPersistedSessionIDLength {
			return ""
		}
	}
	return trimmed
}

// CaptureUsageSessionFromBody runs before provider rewrites and before asynchronous
// recording. Only correlation metadata is read; the body is not retained or changed.
func CaptureUsageSessionFromBody(c *gin.Context, body []byte) {
	if c == nil {
		return
	}
	if _, exists := c.Get("usage_attribution_session_id"); exists {
		return
	}
	// Freeze absence too: provider-generated identifiers must never backfill unknown usage.
	c.Set("usage_attribution_session_id", ExtractUsageSessionFromBody(c, body))
}

// ExtractUsageSessionFromBody prefers per-request/per-frame native metadata over
// handshake headers. It deliberately ignores cached Gin state for WebSocket turns.
func ExtractUsageSessionFromBody(c *gin.Context, body []byte) string {
	for _, path := range []string{"client_metadata.thread_id", "client_metadata.x-codex-turn-metadata", "client_metadata.session_id"} {
		raw := gjson.GetBytes(body, path)
		id := raw.String()
		if path == "client_metadata.x-codex-turn-metadata" {
			if raw.IsObject() {
				id = raw.Get("thread_id").String()
			} else {
				id = codexTurnMetadataThreadID(raw.String())
			}
		}
		if id = sanitizeSessionID(id); id != "" {
			return id
		}
	}
	raw := gjson.GetBytes(body, "metadata.user_id")
	var id string
	if raw.Type == gjson.String {
		if parsed := ParseMetadataUserID(raw.String()); parsed != nil {
			id = parsed.SessionID
		}
	} else if raw.IsObject() {
		id = raw.Get("session_id").String()
	}
	if id = sanitizeSessionID(id); id != "" {
		return id
	}
	return extractUsageSessionHeaders(c)
}
