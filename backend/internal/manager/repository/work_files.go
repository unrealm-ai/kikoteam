package repository

import (
	"context"
	"database/sql"
	"encoding/base64"
	"path"
	"unicode/utf8"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type workFile struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Digest string `json:"sha256"`
	Size   int64  `json:"size_bytes"`
	Grant  string `json:"-"`
}

// Follow just the explicitly forwarded selection for this file. Revoking an
// earlier hop fences both a read and a subsequent grant, even with cached bytes.
func assignedFiles(ctx context.Context, tx *sql.Tx, src workSource, w workItem) ([]workFile, error) {
	rows, err := tx.QueryContext(ctx, `SELECT f.id,f.name,f.digest,f.size_bytes,s.boundary_id FROM manager_work_file_selections s
 JOIN manager_work_files f ON f.id=s.file_id WHERE s.boundary_id=CASE WHEN $1='review' THEN NULLIF($2,'')::uuid
 ELSE (SELECT a.boundary_id FROM manager_work_assignments a WHERE a.id=NULLIF($3,'')::uuid AND a.session_id=$4) END AND manager_work_file_allowed(s.boundary_id,f.id) ORDER BY f.name,f.id`, src.role, w.submission, w.assignment, src.session)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration errors are returned.
	files := []workFile{}
	for rows.Next() {
		var f workFile
		if err = rows.Scan(&f.ID, &f.Name, &f.Digest, &f.Size, &f.Grant); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}
func (s *Store) selectWorkFiles(ctx context.Context, tx *sql.Tx, src workSource, w workItem, boundary string, a service.WorkArguments) error {
	if len(a.FileIDs)+len(a.FilePaths) == 0 {
		return nil
	}
	if s.WorkAssets == nil {
		return service.Fail("work_files_unavailable", 503, "文件版本存储不可用")
	}
	files, err := assignedFiles(ctx, tx, src, w)
	if err != nil {
		return err
	}
	available := map[string]workFile{}
	for _, f := range files {
		available[f.ID] = f
	}
	for _, id := range a.FileIDs {
		f, ok := available[id]
		if !ok {
			return service.Fail("work_file_forbidden", 403, "未获准转交此文件版本")
		}
		delete(available, id)
		if _, err = tx.ExecContext(ctx, `INSERT INTO manager_work_file_selections(boundary_id,file_id,parent_boundary_id) VALUES($1,$2,$3)`, boundary, id, f.Grant); err != nil {
			return err
		}
	}
	var conversation, space, task string
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(conversation_id::text,''),COALESCE(task_space_id::text,''),task_id FROM manager_sessions WHERE id=$1`, src.session).Scan(&conversation, &space, &task); err != nil {
		return err
	}
	seenPaths := map[string]bool{}
	for _, p := range a.FilePaths {
		if seenPaths[p] {
			return service.Fail("invalid_work_file", 400, "文件选择重复")
		}
		seenPaths[p] = true
		data, e := service.ReadWorkFile(s.RuntimeRoot, src.workspace, src.agent, conversation, space, task, p)
		if e != nil {
			return e
		}
		digest, e := s.WorkAssets.PutWorkFile(ctx, src.workspace, data)
		if e != nil {
			return e
		}
		id := service.NewID()
		if _, err = tx.ExecContext(ctx, `INSERT INTO manager_work_files(id,boundary_id,name,digest,size_bytes) VALUES($1,$2,$3,$4,$5)`, id, boundary, path.Base(p), digest, len(data)); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO manager_work_file_selections(boundary_id,file_id) VALUES($1,$2)`, boundary, id); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) readAssignedFile(ctx context.Context, tx *sql.Tx, src workSource, w workItem, a service.WorkArguments) (any, error) {
	if !service.ValidID(a.FileID) || a.Offset < 0 {
		return nil, service.Fail("invalid_work_file", 400, "文件标识或偏移无效")
	}
	files, err := assignedFiles(ctx, tx, src, w)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f.ID != a.FileID {
			continue
		}
		if a.Offset > f.Size {
			return nil, service.Fail("invalid_work_file", 400, "已超出文件长度")
		}
		if s.WorkAssets == nil {
			return nil, service.Fail("work_files_unavailable", 503, "文件版本存储不可用")
		}
		data, e := s.WorkAssets.GetWorkFile(ctx, src.workspace, f.Digest, f.Size)
		if e != nil {
			return nil, e
		}
		end := min(f.Size, a.Offset+48*1024)
		part := data[a.Offset:end]
		result := map[string]any{"file": f, "offset": a.Offset, "data_base64": base64.StdEncoding.EncodeToString(part), "eof": end == f.Size}
		if end < f.Size {
			result["next_offset"] = end
		}
		if utf8.Valid(part) {
			result["text"] = string(part)
		}
		return result, nil
	}
	return nil, service.Fail("work_file_forbidden", 403, "当前环节未获准读取此文件版本")
}

// A sender can lose a previous hop's grant after preparing this boundary.
func validateForwardedFiles(ctx context.Context, tx *sql.Tx, boundary string) error {
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT FROM manager_work_file_selections selected
 WHERE selected.boundary_id=$1 AND selected.parent_boundary_id IS NOT NULL AND NOT manager_work_file_allowed(selected.parent_boundary_id,selected.file_id))`, boundary).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return service.Fail("work_file_forbidden", 403, "转交文件的来源授权已失效")
	}
	return nil
}
