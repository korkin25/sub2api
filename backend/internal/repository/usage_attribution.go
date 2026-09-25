package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *usageLogRepository) RegisterAttribution(ctx context.Context, userID, keyID int64, in service.AttributionRegistration) error {
	if err := in.Validate(); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if in.ParentSessionID != "" {
		var project, task, client, host string
		err = tx.QueryRowContext(ctx, `SELECT project_id,task_id,client_kind,COALESCE(host,'') FROM usage_attribution_sessions WHERE user_id=$1 AND api_key_id=$2 AND session_id=$3`, userID, keyID, in.ParentSessionID).Scan(&project, &task, &client, &host)
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrAttributionParentNotFound
		}
		if err != nil {
			return err
		}
		if (in.ProjectID != "" && in.ProjectID != project) || (in.TaskID != "" && in.TaskID != task) || in.ClientKind != client {
			return service.ErrAttributionConflict
		}
		in.ProjectID = project
		in.TaskID = task
		if in.Host == "" {
			in.Host = host
		}
	}
	var accepted, boundProject, boundTask string
	err = tx.QueryRowContext(ctx, `INSERT INTO usage_attribution_sessions(user_id,api_key_id,session_id,project_id,task_id,client_kind,host,parent_session_id)
 SELECT $1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,'') WHERE EXISTS(SELECT 1 FROM api_keys WHERE id=$2 AND user_id=$1)
 ON CONFLICT(user_id,api_key_id,session_id) DO UPDATE SET session_id=EXCLUDED.session_id
 WHERE usage_attribution_sessions.client_kind=EXCLUDED.client_kind
 AND ($9 OR (usage_attribution_sessions.project_id=EXCLUDED.project_id AND usage_attribution_sessions.task_id=EXCLUDED.task_id
 AND usage_attribution_sessions.parent_session_id IS NOT DISTINCT FROM EXCLUDED.parent_session_id))
 RETURNING session_id,project_id,task_id`, userID, keyID, in.SessionID, in.ProjectID, in.TaskID, in.ClientKind, in.Host, in.ParentSessionID, in.PreserveExisting).Scan(&accepted, &boundProject, &boundTask)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrAttributionConflict
	}
	if err != nil {
		return err
	}
	// The UPSERT returns the winning immutable IDs, including a concurrently
	// registered child. Never initialize labels for discarded resume defaults.
	if in.PreserveExisting && (boundProject != in.ProjectID || boundTask != in.TaskID) {
		return tx.Commit()
	}
	for _, n := range []service.AttributionName{{UserID: userID, Kind: "project", ID: in.ProjectID, Name: in.ProjectName}, {UserID: userID, Kind: "task", ID: in.TaskID, Name: in.TaskName}} {
		if n.Name == "" {
			n.Name = n.ID
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO usage_attribution_names(user_id,kind,id,name) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, n.UserID, n.Kind, n.ID, n.Name); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (r *usageLogRepository) RenameAttribution(ctx context.Context, n service.AttributionName) error {
	if err := n.Validate(); err != nil {
		return err
	}
	res, err := r.sql.ExecContext(ctx, `UPDATE usage_attribution_names SET name=$4 WHERE user_id=$1 AND kind=$2 AND id=$3`, n.UserID, n.Kind, n.ID, n.Name)
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if err == nil && count == 0 {
		return service.ErrUsageLogNotFound
	}
	return err
}

func attributionWhere(f service.AttributionFilter) (string, []any) {
	args := []any{f.Start, f.End}
	parts := []string{"ul.created_at >= $1", "ul.created_at < $2"}
	add := func(col string, v any) {
		args = append(args, v)
		parts = append(parts, fmt.Sprintf("%s=$%d", col, len(args)))
	}
	if f.UserID > 0 {
		add("ul.user_id", f.UserID)
	}
	if f.APIKeyID > 0 {
		add("ul.api_key_id", f.APIKeyID)
	}
	for _, p := range [][2]string{{"project_id", f.ProjectID}, {"task_id", f.TaskID}, {"client_kind", f.ClientKind}, {"host", f.Host}} {
		if p[1] != "" {
			add("ul.attribution->>'"+p[0]+"'", p[1])
		}
	}
	if f.Model != "" {
		add("COALESCE(NULLIF(ul.requested_model,''),ul.model)", f.Model)
	}
	if f.Unknown {
		parts = append(parts, "ul.attribution IS NULL")
	}
	return strings.Join(parts, " AND "), args
}

const attributionMeasuresSQL = `COUNT(*),COALESCE(SUM(ul.input_tokens),0),COALESCE(SUM(ul.output_tokens),0),COALESCE(SUM(ul.cache_creation_tokens),0),COALESCE(SUM(ul.cache_read_tokens),0),COALESCE(SUM(ul.input_tokens::bigint+ul.output_tokens+ul.cache_creation_tokens+ul.cache_read_tokens),0),COALESCE(SUM(ul.total_cost),0)::text,COALESCE(SUM(ul.actual_cost),0)::text`

func attributionDest(m *service.AttributionMeasures) []any {
	return []any{&m.Requests, &m.InputTokens, &m.OutputTokens, &m.CacheCreationTokens, &m.CacheReadTokens, &m.TotalTokens, &m.TotalCost, &m.ActualCost}
}
func attributionRates(m *service.AttributionMeasures, minutes float64) {
	if minutes > 0 {
		m.RPM = float64(m.Requests) / minutes
		m.TPM = float64(m.TotalTokens) / minutes
	}
}
func (r *usageLogRepository) AttributionReport(ctx context.Context, f service.AttributionFilter) (*service.AttributionReport, error) {
	if !f.Start.Before(f.End) || f.End.Sub(f.Start) > 366*24*time.Hour {
		return nil, fmt.Errorf("invalid attribution time window")
	}
	dimensions := map[string]string{"project": "ul.attribution->>'project_id'", "task": "ul.attribution->>'task_id'", "client": "ul.attribution->>'client_kind'", "host": "ul.attribution->>'host'", "api_key": "ul.api_key_id::text", "model": "COALESCE(NULLIF(ul.requested_model,''),ul.model)", "user": "ul.user_id::text"}
	dim, ok := dimensions[f.GroupBy]
	if !ok {
		return nil, fmt.Errorf("invalid group_by")
	}
	where, args := attributionWhere(f)
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := &service.AttributionReport{Groups: []service.AttributionGroup{}, Series: []service.AttributionPoint{}, WindowStart: f.Start, WindowEnd: f.End, WindowMinutes: f.End.Sub(f.Start).Minutes()}
	if err = tx.QueryRowContext(ctx, "SELECT "+attributionMeasuresSQL+" FROM usage_logs ul WHERE "+where, args...).Scan(attributionDest(&out.Totals)...); err != nil {
		return nil, err
	}
	attributionRates(&out.Totals, out.WindowMinutes)
	name := "g.id"
	switch f.GroupBy {
	case "project", "task":
		name = "(SELECT n.name FROM usage_attribution_names n WHERE n.user_id=g.user_id AND n.kind='" + f.GroupBy + "' AND n.id=g.id)"
	case "api_key":
		name = "(SELECT k.name FROM api_keys k WHERE k.id::text=g.id AND k.user_id=g.user_id)"
	case "user":
		name = "(SELECT NULLIF(u.username,'') FROM users u WHERE u.id=g.user_id)"
	}
	measureNames := "requests,input_tokens,output_tokens,cache_creation_tokens,cache_read_tokens,total_tokens,total_cost,actual_cost"
	grouped := "WITH g(user_id,id," + measureNames + ") AS (SELECT ul.user_id," + dim + "," + attributionMeasuresSQL + " FROM usage_logs ul WHERE " + where + " GROUP BY ul.user_id," + dim + ") SELECT g.user_id,g.id," + name + "," + measureNames + " FROM g ORDER BY actual_cost::numeric DESC,user_id,id"
	rows, err := tx.QueryContext(ctx, grouped, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var g service.AttributionGroup
		dest := append([]any{&g.UserID, &g.ID, &g.Name}, attributionDest(&g.AttributionMeasures)...)
		if err = rows.Scan(dest...); err != nil {
			rows.Close()
			return nil, err
		}
		attributionRates(&g.AttributionMeasures, out.WindowMinutes)
		out.Groups = append(out.Groups, g)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, "SELECT to_char(ul.created_at AT TIME ZONE 'UTC','YYYY-MM-DD'),"+attributionMeasuresSQL+" FROM usage_logs ul WHERE "+where+" GROUP BY 1 ORDER BY 1", args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p service.AttributionPoint
		if err = rows.Scan(append([]any{&p.Date}, attributionDest(&p.AttributionMeasures)...)...); err != nil {
			rows.Close()
			return nil, err
		}
		day, _ := time.Parse("2006-01-02", p.Date)
		start, end := day, day.AddDate(0, 0, 1)
		if start.Before(f.Start) {
			start = f.Start
		}
		if end.After(f.End) {
			end = f.End
		}
		attributionRates(&p.AttributionMeasures, end.Sub(start).Minutes())
		out.Series = append(out.Series, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// AttributionOptions fetches bounded dimension choices in one usage scan, rather
// than running totals/group/series aggregation once per selector.
func (r *usageLogRepository) AttributionOptions(ctx context.Context, f service.AttributionFilter) (*service.AttributionOptions, error) {
	where, args := attributionWhere(f)
	query := `WITH d AS (SELECT DISTINCT ul.user_id,ul.attribution->>'project_id' AS project_id,ul.attribution->>'task_id' AS task_id,ul.attribution->>'client_kind' AS client_kind,ul.attribution->>'host' AS host FROM usage_logs ul WHERE ` + where + ` ORDER BY 1,2,3,4,5 LIMIT 5001)
 SELECT d.user_id,COALESCE(d.project_id,''),COALESCE(p.name,d.project_id,''),COALESCE(d.task_id,''),COALESCE(t.name,d.task_id,''),COALESCE(d.client_kind,''),COALESCE(d.host,''),COALESCE(u.username,'')
 FROM d LEFT JOIN usage_attribution_names p ON p.user_id=d.user_id AND p.kind='project' AND p.id=d.project_id
 LEFT JOIN usage_attribution_names t ON t.user_id=d.user_id AND t.kind='task' AND t.id=d.task_id
 LEFT JOIN users u ON u.id=d.user_id ORDER BY d.user_id,d.project_id,d.task_id,d.client_kind,d.host`
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &service.AttributionOptions{Projects: []service.AttributionOption{}, Tasks: []service.AttributionOption{}, Clients: []service.AttributionOption{}, Hosts: []service.AttributionOption{}, Users: []service.AttributionOption{}}
	seen := map[string]bool{}
	count := 0
	add := func(kind, id, name string, owner int64, dest *[]service.AttributionOption) {
		if id == "" {
			return
		}
		key := fmt.Sprintf("%s:%d:%s", kind, owner, id)
		if seen[key] {
			return
		}
		seen[key] = true
		*dest = append(*dest, service.AttributionOption{UserID: owner, ID: id, Name: name})
	}
	for rows.Next() {
		count++
		if count > 5000 {
			out.Truncated = true
			break
		}
		var owner int64
		var project, pname, task, tname, client, host, user string
		if err = rows.Scan(&owner, &project, &pname, &task, &tname, &client, &host, &user); err != nil {
			return nil, err
		}
		add("p", project, pname, owner, &out.Projects)
		add("t", task, tname, owner, &out.Tasks)
		add("c", client, client, owner, &out.Clients)
		add("h", host, host, owner, &out.Hosts)
		add("u", fmt.Sprint(owner), user, owner, &out.Users)
	}
	return out, rows.Err()
}
