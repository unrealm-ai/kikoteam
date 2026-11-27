package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"slices"
	"strings"
	"unicode/utf8"
)

const triggerColumns = `id,workspace_id,agent_id,kind,enabled,version,config,allowed_actions,created_by,created_at,updated_at,next_due,notification_targets,name,last_verified_at`

func scanTrigger(row scanner) (service.Trigger, error) {
	var b service.Trigger
	var actions []byte
	err := row.Scan(&b.ID, &b.WorkspaceID, &b.AgentID, &b.Kind, &b.Enabled, &b.Version, &b.Config, &actions, &b.CreatedBy, &b.CreatedAt, &b.UpdatedAt, &b.NextDue, &b.NotificationTargets, &b.Name, &b.LastVerifiedAt)
	if err == nil {
		err = json.Unmarshal(actions, &b.AllowedActions)
	}
	return b, visibleError(err)
}
func lockTriggerAdmin(ctx context.Context, tx *sql.Tx, w, actor, agent string) error {
	var id string
	if err := tx.QueryRowContext(ctx, `SELECT m.user_id FROM platform_members m JOIN platform_workspaces w ON w.id=m.workspace_id WHERE m.workspace_id=$1 AND m.user_id=$2 AND m.status='active' AND platform_can_manage_agent(m.workspace_id,$3,m.user_id) FOR SHARE OF m,w`, w, actor, agent).Scan(&id); err != nil {
		return visibleError(err)
	}
	return visibleError(tx.QueryRowContext(ctx, `SELECT id FROM platform_agents WHERE workspace_id=$1 AND id=$2 FOR SHARE`, w, agent).Scan(&id))
}
func validateTriggerWrite(q service.TriggerWrite) error {
	if q.Kind != "schedule" && q.Kind != "webhook" || len(q.AllowedActions) == 0 || len(q.AllowedActions) > 3 || len(q.Config) > 16384 {
		return service.Fail("invalid_trigger_configuration", 400, "触发器配置无效")
	}

	if q.Kind == "schedule" {
		c, e := service.ParseTriggerSchedule(q.Config)
		if e != nil {
			return e
		}
		action := c.Action
		if action == "" {
			action = "create_task"
		}
		if len(q.AllowedActions) != 1 || q.AllowedActions[0] != action {
			return service.Fail("invalid_trigger_configuration", 400, "定时动作不匹配")
		}
		if action == "send_message" {
			var targets []json.RawMessage
			if e = json.Unmarshal(q.NotificationTargets, &targets); e != nil || len(targets) == 0 {
				return service.Fail("invalid_notification_targets", 400, "固定提醒必须指定通知目标")
			}
		}
		return nil
	}
	seen := map[string]bool{}
	for _, a := range q.AllowedActions {
		if seen[a] || !slices.Contains([]string{"create_task", "supplement_task", "cancel_task"}, a) {
			return service.Fail("invalid_trigger_configuration", 400, "动作无效")
		}
		seen[a] = true
	}

	if eventConfig(q.Config).Adapter == "event" {
		return validateEventConfig(q)
	}
	var c service.TriggerWebhookConfig
	if err := strictJSON(q.Config, &c); err != nil {
		return service.Fail("invalid_trigger_configuration", 400, "Hook配置无效")
	}
	switch c.Adapter {
	case "generic":
		if c.RepositoryID != 0 || c.InstallationID != nil {
			return service.Fail("invalid_trigger_configuration", 400, "通用Hook不可含GitHub配置")
		}
	case "github_issue":
		if !service.ValidGithubID(c.RepositoryID) || c.InstallationID != nil && !service.ValidGithubID(*c.InstallationID) || len(q.AllowedActions) != 1 || q.AllowedActions[0] != "create_task" {
			return service.Fail("invalid_trigger_configuration", 400, "GitHub配置无效")
		}
	default:
		return service.Fail("invalid_trigger_configuration", 400, "Hook协议无效")
	}

	return nil
}
func (s *Store) SaveTrigger(ctx context.Context, w, actor, agent, id string, q service.TriggerWrite) (service.Trigger, error) {
	var b service.Trigger
	if q.Name == "" {
		q.Name = "触发器"
	}
	if strings.TrimSpace(q.Name) != q.Name || strings.ContainsRune(q.Name, 0) || utf8.RuneCountInString(q.Name) > 100 {
		return b, service.Fail("invalid_trigger_configuration", 400, "名称无效")
	}
	if !service.ValidID(w) || !service.ValidID(agent) || (id != "" && !service.ValidID(id)) {
		return b, service.Fail("invalid_argument", 400, "标识无效")
	}
	if err := validateTriggerWrite(q); err != nil {
		return b, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return b, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = lockTriggerAdmin(ctx, tx, w, actor, agent); err != nil {
		return b, err
	}
	if len(q.NotificationTargets) == 0 {
		q.NotificationTargets = json.RawMessage(`[]`)
	}
	if err = ValidateTriggerTargetsTx(ctx, tx, w, actor, agent, q.NotificationTargets); err != nil {
		return b, err
	}
	actions, err := json.Marshal(q.AllowedActions)
	if err != nil {
		return b, err
	}
	var first *service.TriggerCredential
	if id == "" {
		id = service.NewID()
		first, err = s.createTrigger(ctx, tx, w, actor, agent, id, q, actions)
	} else {
		err = s.updateTrigger(ctx, tx, w, agent, id, q, actions)
	}
	if err != nil {
		return b, err
	}
	b, err = scanTrigger(tx.QueryRowContext(ctx, `SELECT `+triggerColumns+` FROM manager_triggers WHERE id=$1`, id))
	if err != nil {
		return b, err
	}
	if err = tx.Commit(); err != nil {
		return service.Trigger{}, err
	}
	b.InitialCredential = first
	return b, nil
}

func (s *Store) createTrigger(ctx context.Context, tx *sql.Tx, w, actor, agent, id string, q service.TriggerWrite, actions []byte) (*service.TriggerCredential, error) {
	if q.Version != 0 || q.SigningToken != nil {
		return nil, service.Fail("invalid_trigger_configuration", 400, "新建触发器的版本或签名凭据无效")
	}
	imported := eventConfig(q.Config).Source == "gitlab"
	if imported && q.Enabled {
		return nil, service.Fail("trigger_credential_required", 409, "请先获取接收地址，再保存 GitLab Signing token 并启用")
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM manager_triggers WHERE workspace_id=$1 AND agent_id=$2`, w, agent).Scan(&count); err != nil {
		return nil, err
	}
	if count >= 100 {
		return nil, service.Fail("trigger_limit", 409, "触发器数量达到上限")
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO manager_triggers(id,workspace_id,agent_id,kind,enabled,config,allowed_actions,created_by,notification_targets,name) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, w, agent, q.Kind, q.Enabled, q.Config, actions, actor, q.NotificationTargets, q.Name)
	if err != nil || q.Kind != "webhook" || imported {
		return nil, err
	}
	credential, err := s.insertTriggerCredential(ctx, tx, id, "")
	return &credential, err
}

func (s *Store) updateTrigger(ctx context.Context, tx *sql.Tx, w, agent, id string, q service.TriggerWrite, actions []byte) error {
	old, err := scanTrigger(tx.QueryRowContext(ctx, `SELECT `+triggerColumns+` FROM manager_triggers WHERE workspace_id=$1 AND agent_id=$2 AND id=$3 FOR UPDATE`, w, agent, id))
	if err != nil {
		return err
	}
	if old.Version != q.Version {
		return service.Fail("version_conflict", 409, "配置已修改")
	}
	if old.Kind == "webhook" && changedHookProtocol(old.Config, q.Config) {
		return service.Fail("trigger_protocol_immutable", 409, "不能更改Hook协议或来源")
	}
	if old.Kind != q.Kind {
		return service.Fail("trigger_protocol_immutable", 409, "不能更改触发器类型")
	}
	if q.SigningToken != nil {
		if old.Kind != "webhook" || eventConfig(old.Config).Source != "gitlab" || !q.Enabled || !service.ValidGitLabSigningToken(*q.SigningToken) {
			return service.Fail("invalid_signing_token", 400, "启用 GitLab 签名模式需要有效的 Signing token")
		}
		if _, err = s.insertTriggerCredential(ctx, tx, id, *q.SigningToken); err != nil {
			return err
		}
	}
	if q.Enabled && q.Kind == "webhook" {
		var has bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_trigger_credentials WHERE trigger_id=$1 AND status='active')`, id).Scan(&has); err != nil {
			return err
		}
		if !has {
			return service.Fail("trigger_credential_required", 409, "缺少有效凭据")
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE manager_triggers SET enabled=$2,config=$3,allowed_actions=$4,notification_targets=$5,name=$6,version=version+1,next_due=NULL,updated_at=now() WHERE id=$1`, id, q.Enabled, q.Config, actions, q.NotificationTargets, q.Name)
	if err != nil {
		return err
	}
	return s.stopTriggerSessions(ctx, tx, old)
}
func (s *Store) GetTrigger(ctx context.Context, w, actor, agent, id string) (service.Trigger, error) {
	var out service.Trigger
	err := s.readTriggerAdmin(ctx, w, actor, agent, func(tx *sql.Tx) error {
		var e error
		out, e = scanTrigger(tx.QueryRowContext(ctx, `SELECT `+triggerColumns+` FROM manager_triggers WHERE workspace_id=$1 AND agent_id=$2 AND id=$3`, w, agent, id))
		return e
	})
	return out, err
}
func (s *Store) readTriggerAdmin(ctx context.Context, w, actor, agent string, fn func(*sql.Tx) error) error {
	if !service.ValidID(w) || !service.ValidID(agent) {
		return service.Fail("invalid_argument", 400, "标识无效")
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if e = lockTriggerAdmin(ctx, tx, w, actor, agent); e != nil {
		return e
	}
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) ListTriggers(ctx context.Context, w, actor, agent string) ([]service.Trigger, error) {
	out := []service.Trigger{}
	err := s.readTriggerAdmin(ctx, w, actor, agent, func(tx *sql.Tx) error {
		rows, e := tx.QueryContext(ctx, `SELECT `+triggerColumns+` FROM manager_triggers WHERE workspace_id=$1 AND agent_id=$2 ORDER BY created_at DESC,id DESC LIMIT 100`, w, agent)
		if e != nil {
			return e
		}
		defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close below.
		for rows.Next() {
			v, x := scanTrigger(rows)
			if x != nil {
				return errors.Join(x, rows.Close())
			}
			out = append(out, v)
		}
		return errors.Join(rows.Err(), rows.Close())
	})
	return out, err
}
