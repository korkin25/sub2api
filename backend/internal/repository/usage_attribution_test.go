package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAttributionFilterParameterized(t *testing.T) {
	f := service.AttributionFilter{Start: time.Now(), End: time.Now().Add(time.Hour), UserID: 7, APIKeyID: 23, ProjectID: "p' OR 1=1--", TaskID: "t", Unknown: true}
	query, args := attributionWhere(f)
	require.NotContains(t, query, f.ProjectID)
	require.Contains(t, query, "ul.user_id=$3")
	require.Contains(t, query, "ul.api_key_id=$4")
	require.Contains(t, query, "ul.attribution IS NULL")
	require.Contains(t, args, f.ProjectID)
}
func TestAttributionRegistryConflictRollsBack(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	r := newUsageLogRepositoryWithSQL(nil, db)
	m.ExpectBegin()
	m.ExpectQuery("INSERT INTO usage_attribution_sessions").WithArgs(int64(1), int64(23), "thread", "p", "t", "codex", "", "", false).WillReturnError(sql.ErrNoRows)
	m.ExpectRollback()
	err = r.RegisterAttribution(context.Background(), 1, 23, service.AttributionRegistration{SessionID: "thread", ProjectID: "p", TaskID: "t", ClientKind: "codex"})
	require.ErrorIs(t, err, service.ErrAttributionConflict)
	require.NoError(t, m.ExpectationsWereMet())
}
func TestAttributionChildInheritsWithinKey(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	r := newUsageLogRepositoryWithSQL(nil, db)
	m.ExpectBegin()
	m.ExpectQuery("SELECT project_id,task_id,client_kind").WithArgs(int64(1), int64(24), "parent").WillReturnRows(sqlmock.NewRows([]string{"project_id", "task_id", "client_kind", "host"}).AddRow("p", "root", "codex", "host"))
	m.ExpectQuery("INSERT INTO usage_attribution_sessions").WithArgs(int64(1), int64(24), "child", "p", "root", "codex", "host", "parent", false).WillReturnRows(sqlmock.NewRows([]string{"session_id", "project_id", "task_id"}).AddRow("child", "p", "root"))
	m.ExpectExec("INSERT INTO usage_attribution_names").WithArgs(int64(1), "project", "p", "p").WillReturnResult(sqlmock.NewResult(0, 0))
	m.ExpectExec("INSERT INTO usage_attribution_names").WithArgs(int64(1), "task", "root", "root").WillReturnResult(sqlmock.NewResult(0, 0))
	m.ExpectCommit()
	err = r.RegisterAttribution(context.Background(), 1, 24, service.AttributionRegistration{SessionID: "child", ParentSessionID: "parent", ClientKind: "codex"})
	require.NoError(t, err)
	require.NoError(t, m.ExpectationsWereMet())
}
func TestAttributionReportPrecisionAndWindowRates(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	r := newUsageLogRepositoryWithSQL(nil, db)
	start := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Minute)
	cols := []string{"requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "total_cost", "actual_cost"}
	m.ExpectBegin()
	m.ExpectQuery("SELECT COUNT").WithArgs(start, end, int64(1)).WillReturnRows(sqlmock.NewRows(cols).AddRow(3, 100, 20, 30, 50, 200, "0.123456789012", "0.023456789012"))
	m.ExpectQuery("WITH g").WithArgs(start, end, int64(1)).WillReturnRows(sqlmock.NewRows(append([]string{"user_id", "id", "name"}, cols...)).AddRow(1, "p", "Renamed", 2, 80, 20, 30, 50, 180, "0.120000000000", "0.020000000000").AddRow(1, nil, nil, 1, 20, 0, 0, 0, 20, "0.003456789012", "0.003456789012"))
	m.ExpectQuery("SELECT to_char").WithArgs(start, end, int64(1)).WillReturnRows(sqlmock.NewRows(append([]string{"date"}, cols...)).AddRow("2026-09-25", 3, 100, 20, 30, 50, 200, "0.123456789012", "0.023456789012"))
	m.ExpectCommit()
	report, err := r.AttributionReport(context.Background(), service.AttributionFilter{Start: start, End: end, UserID: 1, GroupBy: "project"})
	require.NoError(t, err)
	require.Equal(t, "0.123456789012", report.Totals.TotalCost)
	require.Equal(t, 1.5, report.Totals.RPM)
	require.Equal(t, 100.0, report.Totals.TPM)
	require.Equal(t, 100.0, report.Series[0].TPM)
	require.Nil(t, report.Groups[1].ID)
	require.Equal(t, report.Totals.Requests, report.Groups[0].Requests+report.Groups[1].Requests)
	require.NoError(t, m.ExpectationsWereMet())
}
func TestAttributionRenameScopesOwnerAndStableID(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	r := newUsageLogRepositoryWithSQL(nil, db)
	m.ExpectExec("UPDATE usage_attribution_names SET name").WithArgs(int64(1), "task", "root", "New label").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, r.RenameAttribution(context.Background(), service.AttributionName{UserID: 1, Kind: "task", ID: "root", Name: "New label"}))
	require.NoError(t, m.ExpectationsWereMet())
}
