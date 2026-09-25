package usageattribution

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type attributionSpy struct {
	service.UsageLogRepository
	owner, key   int64
	filter       service.AttributionFilter
	name         service.AttributionName
	registration service.AttributionRegistration
}

func (s *attributionSpy) RegisterAttribution(_ context.Context, owner, key int64, r service.AttributionRegistration) error {
	s.owner = owner
	s.key = key
	s.registration = r
	return nil
}
func (s *attributionSpy) RenameAttribution(_ context.Context, n service.AttributionName) error {
	s.name = n
	return nil
}
func (s *attributionSpy) AttributionReport(_ context.Context, f service.AttributionFilter) (*service.AttributionReport, error) {
	s.filter = f
	return &service.AttributionReport{}, nil
}
func TestAttributionRegistrationUsesAuthenticatedKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	spy := &attributionSpy{}
	svc := service.NewUsageService(spy, nil, nil, nil)
	c, w := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"user_id":999,"api_key_id":777,"session_id":"s","project_id":"p","task_id":"t","client_kind":"codex"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 23, UserID: 1})
	Register(c, svc)
	require.Equal(t, int64(1), spy.owner)
	require.Equal(t, int64(23), spy.key)
	_ = w
}
func TestAttributionOwnerCannotQueryOrRenameOtherOwner(t *testing.T) {
	spy := &attributionSpy{}
	svc := service.NewUsageService(spy, nil, nil, nil)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/?user_id=99&api_key_id=24", nil)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
	Report(c, svc, false, false)
	require.Equal(t, int64(1), spy.filter.UserID)
	require.Equal(t, int64(24), spy.filter.APIKeyID)
	c, _ = gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("PATCH", "/", strings.NewReader(`{"user_id":99,"kind":"task","id":"t","name":"renamed"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
	Rename(c, svc, false)
	require.Equal(t, int64(1), spy.name.UserID)
}
func TestAttributionWindowAndFilterValidation(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/?start_date=2026-09-25&end_date=2026-09-25", nil)
	f, err := Filter(c, true, now)
	require.NoError(t, err)
	require.Equal(t, 720.0, f.End.Sub(f.Start).Minutes())
	for _, query := range []string{"api_key_id=-1", "api_key_id=evil", "group_by=project%3BDROP", "unknown=maybe", "start_date=2020-01-01"} {
		c, _ = gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/?"+query, nil)
		_, err = Filter(c, true, now)
		require.Error(t, err, query)
	}
	require.Equal(t, "'=SUM(1)", safeCell("=SUM(1)"))
}

func (s *attributionSpy) AttributionOptions(context.Context, service.AttributionFilter) (*service.AttributionOptions, error) {
	return &service.AttributionOptions{}, nil
}
