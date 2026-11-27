package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func readMaintainerPeople(ctx context.Context, tx *sql.Tx, w, employee string) ([]service.PeopleCandidate, error) {
	items := []service.PeopleCandidate{}
	rows, err := tx.QueryContext(ctx, `
SELECT ref, name, active FROM (
 SELECT jsonb_build_object('kind','user','provider','web','workspace_id',g.workspace_id,'agent_id',g.agent_id,'external_id',g.user_id) ref,
 ac.name, m.status='active' active, g.granted_at
 FROM platform_agent_maintainers g JOIN platform_accounts ac ON ac.id=g.user_id
 JOIN platform_members m ON m.workspace_id=g.workspace_id AND m.user_id=g.user_id
 WHERE g.workspace_id=$1 AND g.agent_id=$2
 UNION ALL
 SELECT g.ref,g.display_name,b.enabled AND b.identity_epoch=g.identity_epoch AND b.identity_verified_generation=b.connection_generation,g.granted_at
 FROM platform_agent_channel_maintainers g JOIN platform_channel_bindings b ON b.id=g.binding_id
 WHERE g.workspace_id=$1 AND g.agent_id=$2
) candidates ORDER BY granted_at,ref->>'provider',ref->>'external_id'`, w, employee)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // Query errors are checked below.
	for rows.Next() {
		var v service.PeopleCandidate
		var raw []byte
		if err = rows.Scan(&raw, &v.DisplayName, &v.Selectable); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &v.Ref); err != nil {
			return nil, err
		}
		v.Status = "unavailable"
		if v.Selectable {
			v.Status = "active"
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

func (s *AgentStore) ReadMaintainers(ctx context.Context, w, actor, employee string) (service.EmployeeMaintainers, error) {
	var out service.EmployeeMaintainers
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback() //nolint:errcheck // Commit owns success.
	if err = AuthorizeEmployeeManagement(ctx, tx, w, employee, actor); err != nil {
		return out, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT maintainers_version FROM platform_agents WHERE workspace_id=$1 AND id=$2", w, employee).Scan(&out.Version); err != nil {
		return out, err
	}
	out.People, err = readMaintainerPeople(ctx, tx, w, employee)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func resolveNewMaintainer(ctx context.Context, tx *sql.Tx, w, employee string, r service.SelectionRef) (service.PeopleCandidate, error) {
	v := service.PeopleCandidate{Ref: r}
	bad := service.Fail("invalid_maintainers", 400, "维护人不属于当前工作区成员或员工渠道，或身份已失效")
	if r.Provider == "web" {
		if !service.ValidWorkspaceSubject(r.ExternalID) || r.BindingID != "" || r.TenantID != "" || r.Namespace != "" || r.Generation != 0 || r.IdentityEpoch != 0 {
			return v, bad
		}
		v.Ref = service.SelectionRef{Kind: "user", Provider: "web", WorkspaceID: w, AgentID: employee, ExternalID: r.ExternalID}
		err := tx.QueryRowContext(ctx, `SELECT ac.name FROM platform_members m JOIN platform_accounts ac ON ac.id=m.user_id
 WHERE m.workspace_id=$1 AND m.user_id=$2 AND m.status='active' FOR SHARE OF m`, w, r.ExternalID).Scan(&v.DisplayName)
		if errors.Is(err, sql.ErrNoRows) {
			return v, bad
		}
		return v, err
	}
	if !service.ValidChannelProvider(r.Provider) || !service.ValidWorkspaceSubject(r.BindingID) {
		return v, bad
	}
	err := (&PeopleSelectorStore{}).resolveChannelPerson(ctx, tx, w, employee, r, &v)
	if errors.Is(err, sql.ErrNoRows) || err == nil && v.Status != "active" && !(r.Provider == "wecom" && v.VerificationStatus == "verified") {
		return v, bad
	}
	return v, err
}

type maintainerKey struct {
	provider, binding, person string
	epoch                     int64
}

func employeeMaintainerKey(r service.SelectionRef) maintainerKey {
	return maintainerKey{r.Provider, r.BindingID, r.ExternalID, r.IdentityEpoch}
}

func insertMaintainer(ctx context.Context, tx *sql.Tx, w, actor, employee string, v service.PeopleCandidate) error {
	r := v.Ref
	if r.Provider == "web" {
		_, err := tx.ExecContext(ctx, `INSERT INTO platform_agent_maintainers(workspace_id,agent_id,user_id,granted_by)
 SELECT $1,$2,m.user_id,$4 FROM platform_members m JOIN platform_agents a ON a.workspace_id=m.workspace_id AND a.id=$2
 WHERE m.workspace_id=$1 AND m.user_id=$3 AND m.status='active' AND m.role NOT IN ('owner','admin') AND a.creator_account_id IS DISTINCT FROM m.user_id`, w, employee, r.ExternalID, actor)
		return err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO platform_agent_channel_maintainers(workspace_id,agent_id,binding_id,identity_epoch,external_id,ref,display_name,granted_by)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, w, employee, r.BindingID, r.IdentityEpoch, r.ExternalID, raw, v.DisplayName, actor)
	return err
}

func deleteMaintainer(ctx context.Context, tx *sql.Tx, employee string, r service.SelectionRef) error {
	if r.Provider == "web" {
		_, err := tx.ExecContext(ctx, "DELETE FROM platform_agent_maintainers WHERE agent_id=$1 AND user_id=$2", employee, r.ExternalID)
		return err
	}
	_, err := tx.ExecContext(ctx, "DELETE FROM platform_agent_channel_maintainers WHERE agent_id=$1 AND binding_id=$2 AND identity_epoch=$3 AND external_id=$4", employee, r.BindingID, r.IdentityEpoch, r.ExternalID)
	return err
}

func replaceMaintainerPeople(ctx context.Context, tx *sql.Tx, w, actor, employee string, refs []service.SelectionRef) ([]service.PeopleCandidate, error) {
	people, err := readMaintainerPeople(ctx, tx, w, employee)
	if err != nil {
		return nil, err
	}
	old := make(map[maintainerKey]service.SelectionRef, len(people))
	for _, p := range people {
		old[employeeMaintainerKey(p.Ref)] = p.Ref
	}
	seen := make(map[maintainerKey]bool, len(refs))
	for _, ref := range refs {
		k := employeeMaintainerKey(ref)
		if seen[k] {
			continue
		}
		seen[k] = true
		if _, exists := old[k]; exists {
			continue
		}
		candidate, err := resolveNewMaintainer(ctx, tx, w, employee, ref)
		if err != nil {
			return nil, err
		}
		if err = insertMaintainer(ctx, tx, w, actor, employee, candidate); err != nil {
			return nil, err
		}
	}
	for k, ref := range old {
		if !seen[k] {
			if err = deleteMaintainer(ctx, tx, employee, ref); err != nil {
				return nil, err
			}
		}
	}
	return people, nil
}

func (s *AgentStore) ReplaceMaintainers(ctx context.Context, w, actor, employee string, v service.EmployeeMaintainersUpdate) (service.EmployeeMaintainers, error) {
	var out service.EmployeeMaintainers
	err := s.WithWorkspace(ctx, w, func(raw service.AgentTx) error {
		tx := raw.(*agentTx).model.tx
		if err := AuthorizeEmployeeManagement(ctx, tx, w, employee, actor); err != nil {
			return err
		}
		var version int64
		err := tx.QueryRowContext(ctx, `UPDATE platform_agents SET maintainers_version=maintainers_version+1
 WHERE workspace_id=$1 AND id=$2 AND maintainers_version=$3 RETURNING maintainers_version`, w, employee, v.Version).Scan(&version)
		if errors.Is(err, sql.ErrNoRows) {
			return service.Fail("maintainers_version_conflict", 409, "维护人名单已更新，请刷新后重试")
		}
		if err != nil {
			return err
		}
		before, err := replaceMaintainerPeople(ctx, tx, w, actor, employee, v.People)
		if err != nil {
			return err
		}
		out.Version = version
		out.People, err = readMaintainerPeople(ctx, tx, w, employee)
		if err != nil {
			return err
		}
		return recordMaintainerAudit(ctx, tx, w, actor, employee, version, before, out.People)
	})
	return out, err
}

func subtractMaintainers(a, b []service.PeopleCandidate) []service.SelectionRef {
	known := make(map[maintainerKey]bool, len(b))
	for _, p := range b {
		known[employeeMaintainerKey(p.Ref)] = true
	}
	out := []service.SelectionRef{}
	for _, p := range a {
		if !known[employeeMaintainerKey(p.Ref)] {
			out = append(out, p.Ref)
		}
	}
	return out
}

func recordMaintainerAudit(ctx context.Context, tx *sql.Tx, w, actor, employee string, version int64, before, after []service.PeopleCandidate) error {
	added, err := json.Marshal(subtractMaintainers(after, before))
	if err != nil {
		return err
	}
	removed, err := json.Marshal(subtractMaintainers(before, after))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO platform_agent_maintainer_audit(workspace_id,agent_id,version,actor_account_id,added,removed)
 VALUES($1,$2,$3,$4,$5,$6)`, w, employee, version, actor, added, removed)
	return err
}
