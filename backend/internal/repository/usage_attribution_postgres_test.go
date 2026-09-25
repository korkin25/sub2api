package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/usageattribution"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// This opt-in test accepts only an explicitly supplied disposable database DSN.
// It creates and removes its own schema; it never reads application configuration.
func TestAttributionPostgresHTTP(t *testing.T) {
	dsn := os.Getenv("SUB2API_ATTRIBUTION_TEST_DSN")
	if dsn == "" {
		t.Skip("set SUB2API_ATTRIBUTION_TEST_DSN to a disposable PostgreSQL database")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	schema := fmt.Sprintf("attribution_test_%d", time.Now().UnixNano())
	_, err = db.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	defer db.Exec("DROP SCHEMA " + schema + " CASCADE")
	_, err = db.Exec("SET search_path TO " + schema)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE users(id BIGINT PRIMARY KEY,username TEXT);CREATE TABLE api_keys(id BIGINT PRIMARY KEY,user_id BIGINT REFERENCES users(id),name TEXT);
 CREATE TABLE usage_logs(id BIGSERIAL PRIMARY KEY,user_id BIGINT,api_key_id BIGINT,session_id TEXT,model TEXT NOT NULL DEFAULT 'model',requested_model TEXT,input_tokens INTEGER NOT NULL DEFAULT 0,output_tokens INTEGER NOT NULL DEFAULT 0,cache_creation_tokens INTEGER NOT NULL DEFAULT 0,cache_read_tokens INTEGER NOT NULL DEFAULT 0,total_cost NUMERIC(24,12) NOT NULL DEFAULT 0,actual_cost NUMERIC(24,12) NOT NULL DEFAULT 0,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
 INSERT INTO users VALUES(1,'owner'),(2,'other');INSERT INTO api_keys VALUES(23,1,'csssr'),(24,1,'kk573'),(25,2,'other');`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/234_usage_attribution.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	repo := newUsageLogRepositoryWithSQL(nil, db)
	svc := service.NewUsageService(repo, nil, nil, nil)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	// Authentication is injected exactly at the production middleware context seam;
	// repository, HTTP parsing, scope enforcement, SQL and responses are real.
	router.POST("/register", func(c *gin.Context) {
		key := int64(23)
		owner := int64(1)
		if c.GetHeader("X-Test-Key") == "24" {
			key = 24
		}
		if c.GetHeader("X-Test-Key") == "25" {
			key = 25
			owner = 2
		}
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: key, UserID: owner})
		usageattribution.Register(c, svc)
	})
	router.GET("/report", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		usageattribution.Report(c, svc, false, false)
	})
	router.GET("/options", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		usageattribution.Options(c, svc, false)
	})
	router.GET("/export", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		usageattribution.Report(c, svc, false, true)
	})
	router.PATCH("/names", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		usageattribution.Rename(c, svc, false)
	})
	request := func(method, path, body, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-Key", key)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	root := `{"client_kind":"codex","session_id":"same","project_id":"project-a","task_id":"root-a","project_name":"A","task_name":"Original","host":"host-a","user_id":2,"api_key_id":25}`
	require.Equal(t, 200, request("POST", "/register", root, "23").Code)
	require.Equal(t, 200, request("POST", "/register", root, "23").Code)
	other := strings.ReplaceAll(strings.ReplaceAll(root, "project-a", "project-b"), "root-a", "root-b")
	require.Equal(t, 409, request("POST", "/register", other, "23").Code)
	require.Equal(t, 200, request("POST", "/register", other, "24").Code)
	require.Equal(t, 200, request("POST", "/register", other, "25").Code)
	child := `{"client_kind":"codex","session_id":"child","parent_session_id":"same"}`
	require.Equal(t, 200, request("POST", "/register", child, "23").Code)
	resume := `{"client_kind":"codex","session_id":"child","project_id":"moved-project","task_id":"child","task_name":"Resume default","preserve_existing":true}`
	require.Equal(t, 200, request("POST", "/register", resume, "23").Code)
	require.Equal(t, 409, request("POST", "/register", strings.Replace(resume, "codex", "claude", 1), "23").Code)
	require.Equal(t, 200, request("POST", "/register", resume, "24").Code)
	var actualProject, actualTask string
	require.NoError(t, db.QueryRow(`SELECT project_id,task_id FROM usage_attribution_sessions WHERE user_id=1 AND api_key_id=23 AND session_id='child'`).Scan(&actualProject, &actualTask))
	require.Equal(t, "project-a", actualProject)
	require.Equal(t, "root-a", actualTask)
	require.NoError(t, db.QueryRow(`SELECT project_id,task_id FROM usage_attribution_sessions WHERE user_id=1 AND api_key_id=24 AND session_id='child'`).Scan(&actualProject, &actualTask))
	require.Equal(t, "moved-project", actualProject)
	require.Equal(t, "child", actualTask)

	require.Equal(t, 404, request("POST", "/register", `{"client_kind":"codex","session_id":"bad","parent_session_id":"foreign"}`, "24").Code)
	// Backdate synthetic registrations and rows to make HTTP's capped UTC window deterministic.
	_, err = db.Exec(`UPDATE usage_attribution_sessions SET created_at=NOW()-INTERVAL '2 hours';INSERT INTO usage_logs(user_id,api_key_id,session_id,input_tokens,output_tokens,total_cost,actual_cost,created_at) VALUES
 (1,23,'same',10,5,0.123456789012,0.100000000001,NOW()-INTERVAL '1 hour'),
 (1,23,'child',20,5,0.200000000002,0.200000000002,NOW()-INTERVAL '1 hour'),
 (1,24,'same',30,5,0.300000000003,0.300000000003,NOW()-INTERVAL '1 hour'),
 (1,23,NULL,40,5,0,0,NOW()-INTERVAL '1 hour'),
 (2,25,'same',999,1,9,9,NOW()-INTERVAL '1 hour');`)
	require.NoError(t, err)
	start := time.Now().UTC().Add(-24 * time.Hour)
	end := time.Now().UTC()
	filter := service.AttributionFilter{UserID: 1, GroupBy: "task", Start: start, End: end}
	report, err := repo.AttributionReport(context.Background(), filter)
	require.NoError(t, err)
	require.Equal(t, int64(4), report.Totals.Requests)
	require.Equal(t, int64(120), report.Totals.TotalTokens)
	require.Equal(t, "0.623456789017", report.Totals.TotalCost)
	require.Len(t, report.Groups, 3)
	var grouped int64
	for _, g := range report.Groups {
		grouped += g.Requests
		if g.ID != nil && *g.ID == "root-a" {
			require.Equal(t, int64(2), g.Requests)
		}
	}
	require.Equal(t, report.Totals.Requests, grouped)
	require.Equal(t, report.Totals.Requests, report.Series[0].Requests)
	require.Equal(t, 200, request("PATCH", "/names", `{"user_id":2,"kind":"task","id":"root-a","name":"Renamed"}`, "").Code)
	require.Equal(t, 200, request("POST", "/register", root, "23").Code)
	renamed, err := repo.AttributionReport(context.Background(), filter)
	require.NoError(t, err)
	require.Equal(t, report.Totals, renamed.Totals)
	for _, g := range renamed.Groups {
		if g.ID != nil && *g.ID == "root-a" {
			require.Equal(t, "Renamed", *g.Name)
		}
	}
	for _, dimension := range []string{"project", "task", "client", "host", "api_key", "model", "user"} {
		filter.GroupBy = dimension
		got, err := repo.AttributionReport(context.Background(), filter)
		require.NoError(t, err, dimension)
		require.Equal(t, report.Totals, got.Totals)
	}
	filter.GroupBy = "task"
	filter.APIKeyID = 24
	keyReport, err := repo.AttributionReport(context.Background(), filter)
	require.NoError(t, err)
	require.Equal(t, int64(1), keyReport.Totals.Requests)
	filter.APIKeyID = 25
	foreign, err := repo.AttributionReport(context.Background(), filter)
	require.NoError(t, err)
	require.Zero(t, foreign.Totals.Requests)
	filter.APIKeyID = 0
	filter.Unknown = true
	unknown, err := repo.AttributionReport(context.Background(), filter)
	require.NoError(t, err)
	require.Equal(t, int64(1), unknown.Totals.Requests)
	response := request("GET", "/report?user_id=2&group_by=task", "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	var envelope struct {
		Data service.AttributionReport `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Equal(t, int64(4), envelope.Data.Totals.Requests)
	response = request("GET", "/options", "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), `"user_id":2`)
	require.Contains(t, response.Body.String(), "Renamed")
	response = request("GET", "/export?group_by=task", "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "Renamed")
	require.NotContains(t, response.Body.String(), "9.000000000000")
}
