// Package usageattribution shares the report contract between owner and admin routes.
package usageattribution

import (
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func repository(c *gin.Context, s *service.UsageService) (service.UsageAttributionRepository, bool) {
	r, err := s.AttributionRepository()
	if err != nil {
		response.InternalError(c, "Usage attribution unavailable")
		return nil, false
	}
	return r, true
}
func Register(c *gin.Context, s *service.UsageService) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok {
		response.Unauthorized(c, "API key required")
		return
	}
	var in service.AttributionRegistration
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid registration")
		return
	}
	if err := in.Validate(); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	r, ok := repository(c, s)
	if !ok {
		return
	}
	err := r.RegisterAttribution(c.Request.Context(), key.UserID, key.ID, in)
	if errors.Is(err, service.ErrAttributionConflict) {
		response.Error(c, 409, err.Error())
		return
	}
	if errors.Is(err, service.ErrAttributionParentNotFound) {
		response.Error(c, 404, err.Error())
		return
	}
	if err != nil {
		response.InternalError(c, "Unable to register attribution")
		return
	}
	response.Success(c, gin.H{"registered": true})
}
func positiveQuery(c *gin.Context, name string) (int64, error) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return id, nil
}
func Filter(c *gin.Context, admin bool, now time.Time) (service.AttributionFilter, error) {
	f := service.AttributionFilter{GroupBy: c.DefaultQuery("group_by", "project"), ProjectID: c.Query("project_id"), TaskID: c.Query("task_id"), ClientKind: c.Query("client_kind"), Host: c.Query("host"), Model: c.Query("model")}
	var err error
	if admin {
		f.UserID, err = positiveQuery(c, "user_id")
		if err != nil {
			return f, err
		}
	} else {
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok {
			return f, fmt.Errorf("not authenticated")
		}
		f.UserID = subject.UserID
	}
	f.APIKeyID, err = positiveQuery(c, "api_key_id")
	if err != nil {
		return f, err
	}
	switch f.GroupBy {
	case "project", "task", "client", "host", "api_key", "model":
	case "user":
		if !admin {
			return f, fmt.Errorf("group_by user requires administrator")
		}
	default:
		return f, fmt.Errorf("invalid group_by")
	}
	if raw := c.Query("unknown"); raw != "" {
		f.Unknown, err = strconv.ParseBool(raw)
		if err != nil {
			return f, fmt.Errorf("invalid unknown")
		}
	}
	now = now.UTC()
	f.Start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -6)
	f.End = now
	if raw := c.Query("start_date"); raw != "" {
		f.Start, err = time.Parse("2006-01-02", raw)
		if err != nil {
			return f, fmt.Errorf("invalid start_date")
		}
	}
	if raw := c.Query("end_date"); raw != "" {
		f.End, err = time.Parse("2006-01-02", raw)
		if err != nil {
			return f, fmt.Errorf("invalid end_date")
		}
		f.End = f.End.AddDate(0, 0, 1)
		if f.End.After(now) {
			f.End = now
		}
	}
	if !f.Start.Before(f.End) || f.End.Sub(f.Start) > 366*24*time.Hour {
		return f, fmt.Errorf("date window must be positive and at most 366 days")
	}
	return f, nil
}
func Report(c *gin.Context, s *service.UsageService, admin, export bool) {
	f, err := Filter(c, admin, time.Now())
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	r, ok := repository(c, s)
	if !ok {
		return
	}
	report, err := r.AttributionReport(c.Request.Context(), f)
	if err != nil {
		response.InternalError(c, "Unable to load attribution report")
		return
	}
	if !export {
		response.Success(c, report)
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="usage-attribution.csv"`)
	w := csv.NewWriter(c.Writer)
	_ = w.Write([]string{"user_id", "id", "name", "requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "total_cost", "actual_cost", "rpm", "tpm", "window_start", "window_end"})
	for _, g := range report.Groups {
		id, name := "", ""
		if g.ID != nil {
			id = *g.ID
		}
		if g.Name != nil {
			name = *g.Name
		}
		row := []string{strconv.FormatInt(g.UserID, 10), safeCell(id), safeCell(name)}
		for _, n := range []int64{g.Requests, g.InputTokens, g.OutputTokens, g.CacheCreationTokens, g.CacheReadTokens, g.TotalTokens} {
			row = append(row, strconv.FormatInt(n, 10))
		}
		row = append(row, g.TotalCost, g.ActualCost, strconv.FormatFloat(g.RPM, 'f', 6, 64), strconv.FormatFloat(g.TPM, 'f', 6, 64), report.WindowStart.Format(time.RFC3339), report.WindowEnd.Format(time.RFC3339))
		_ = w.Write(row)
	}
	w.Flush()
}
func safeCell(v string) string {
	if strings.HasPrefix(v, "=") || strings.HasPrefix(v, "+") || strings.HasPrefix(v, "-") || strings.HasPrefix(v, "@") || strings.HasPrefix(v, "\t") || strings.HasPrefix(v, "\r") {
		return "'" + v
	}
	return v
}
func Rename(c *gin.Context, s *service.UsageService, admin bool) {
	var n service.AttributionName
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	if err := c.ShouldBindJSON(&n); err != nil {
		response.BadRequest(c, "Invalid rename")
		return
	}
	if !admin {
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok {
			response.Unauthorized(c, "User not authenticated")
			return
		}
		n.UserID = subject.UserID
	}
	if n.UserID <= 0 {
		response.BadRequest(c, "user_id required")
		return
	}
	if err := n.Validate(); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	r, ok := repository(c, s)
	if !ok {
		return
	}
	if err := r.RenameAttribution(c.Request.Context(), n); err != nil {
		if errors.Is(err, service.ErrUsageLogNotFound) {
			response.NotFound(c, "Attribution name not found")
		} else {
			response.InternalError(c, "Unable to rename attribution")
		}
		return
	}
	response.Success(c, gin.H{"updated": true})
}

func Options(c *gin.Context, s *service.UsageService, admin bool) {
	f, err := Filter(c, admin, time.Now())
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	r, ok := repository(c, s)
	if !ok {
		return
	}
	out, err := r.AttributionOptions(c.Request.Context(), f)
	if err != nil {
		response.InternalError(c, "Unable to load attribution options")
		return
	}
	response.Success(c, out)
}
