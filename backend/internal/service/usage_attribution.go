package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrAttributionConflict = errors.New("session already belongs to another project or task")
var ErrAttributionParentNotFound = errors.New("parent session not registered for this API key")

type AttributionRegistration struct {
	PreserveExisting bool   `json:"preserve_existing"`
	SessionID        string `json:"session_id"`
	ProjectID        string `json:"project_id"`
	ProjectName      string `json:"project_name"`
	TaskID           string `json:"task_id"`
	TaskName         string `json:"task_name"`
	ClientKind       string `json:"client_kind"`
	Host             string `json:"host"`
	ParentSessionID  string `json:"parent_session_id"`
}

func validAttributionText(s string, max int) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > max {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return strings.TrimSpace(s) == s
}
func (r AttributionRegistration) Validate() error {
	if r.SessionID == "" || (r.ClientKind != "codex" && r.ClientKind != "claude") {
		return fmt.Errorf("session_id and client_kind (codex or claude) required")
	}
	for _, v := range []string{r.SessionID, r.ProjectID, r.TaskID, r.Host, r.ParentSessionID} {
		if !validAttributionText(v, 200) {
			return fmt.Errorf("identifiers must be valid text of at most 200 characters")
		}
	}
	if !validAttributionText(r.ProjectName, 300) || !validAttributionText(r.TaskName, 300) {
		return fmt.Errorf("names must be valid text of at most 300 characters")
	}
	if r.ParentSessionID == "" && (r.ProjectID == "" || r.TaskID == "") {
		return fmt.Errorf("project_id and task_id required for root session")
	}
	if r.PreserveExisting && r.ParentSessionID != "" {
		return fmt.Errorf("preserve_existing is only supported for SessionStart without parent_session_id")
	}
	if r.ParentSessionID == r.SessionID {
		return fmt.Errorf("session cannot be its own parent")
	}
	return nil
}

type AttributionFilter struct {
	UserID                                              int64
	APIKeyID                                            int64
	ProjectID, TaskID, ClientKind, Host, Model, GroupBy string
	Unknown                                             bool
	Start, End                                          time.Time
}
type AttributionMeasures struct {
	Requests            int64   `json:"requests"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	TotalTokens         int64   `json:"total_tokens"`
	TotalCost           string  `json:"total_cost"`
	ActualCost          string  `json:"actual_cost"`
	RPM                 float64 `json:"rpm"`
	TPM                 float64 `json:"tpm"`
}
type AttributionGroup struct {
	UserID int64   `json:"user_id"`
	ID     *string `json:"id"`
	Name   *string `json:"name"`
	AttributionMeasures
}
type AttributionPoint struct {
	Date string `json:"date"`
	AttributionMeasures
}
type AttributionReport struct {
	Groups        []AttributionGroup  `json:"groups"`
	Totals        AttributionMeasures `json:"totals"`
	Series        []AttributionPoint  `json:"series"`
	WindowStart   time.Time           `json:"window_start"`
	WindowEnd     time.Time           `json:"window_end"`
	WindowMinutes float64             `json:"window_minutes"`
}
type AttributionName struct {
	UserID int64  `json:"user_id"`
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Name   string `json:"name"`
}

func (n AttributionName) Validate() error {
	if (n.Kind != "project" && n.Kind != "task") || n.ID == "" || strings.TrimSpace(n.Name) == "" || !validAttributionText(n.ID, 200) || !validAttributionText(n.Name, 300) {
		return fmt.Errorf("kind project/task, id and name (max 300 characters) required")
	}
	return nil
}

type UsageAttributionRepository interface {
	AttributionOptions(context.Context, AttributionFilter) (*AttributionOptions, error)
	RegisterAttribution(context.Context, int64, int64, AttributionRegistration) error
	AttributionReport(context.Context, AttributionFilter) (*AttributionReport, error)
	RenameAttribution(context.Context, AttributionName) error
}

func (s *UsageService) AttributionRepository() (UsageAttributionRepository, error) {
	r, ok := s.usageRepo.(UsageAttributionRepository)
	if !ok {
		return nil, fmt.Errorf("usage attribution unavailable")
	}
	return r, nil
}

type AttributionOption struct {
	UserID int64  `json:"user_id"`
	ID     string `json:"id"`
	Name   string `json:"name"`
}
type AttributionOptions struct {
	Projects  []AttributionOption `json:"projects"`
	Tasks     []AttributionOption `json:"tasks"`
	Clients   []AttributionOption `json:"clients"`
	Hosts     []AttributionOption `json:"hosts"`
	Users     []AttributionOption `json:"users"`
	Truncated bool                `json:"truncated"`
}
