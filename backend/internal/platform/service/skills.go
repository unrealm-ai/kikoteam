package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

func skillAuthorize(ctx context.Context, t SkillTx, actor string, write bool) error {
	m, e := t.Member(ctx, actor)
	if errors.Is(e, ErrWorkspaceMissing) || e == nil && (m.Status != "active" || write && m.Role != "owner" && m.Role != "admin") {
		return Fail("workspace_forbidden", 403, "没有 Skill 管理权限")
	}
	return e
}
func (s *SkillService) transaction(ctx context.Context, w, a string, write bool, fn func(SkillTx) error) error {
	return s.Store.WithWorkspace(ctx, w, func(t SkillTx) error {
		if e := skillAuthorize(ctx, t, a, write); e != nil {
			return e
		}
		return fn(t)
	})
}
func (s *SkillService) List(ctx context.Context, w, a string, p, n int, filter ResourceListFilter) (SkillPage, error) {
	out := SkillPage{Items: []Skill{}, Page: p, PageSize: n}
	if err := filter.normalize(); err != nil {
		return out, err
	}
	if e := pageValid(p, n); e != nil {
		return out, e
	}
	e := s.transaction(ctx, w, a, false, func(t SkillTx) error { var e error; out.Items, out.Total, e = t.List(ctx, p, n, filter); return e })
	return out, e
}
func (s *SkillService) Get(ctx context.Context, w, a, id string) (Skill, error) {
	var out Skill
	e := s.transaction(ctx, w, a, false, func(t SkillTx) error { var e error; out, e = t.Get(ctx, id); return e })
	return out, e
}
func (s *SkillService) Versions(ctx context.Context, w, a, id string, p, n int) ([]SkillVersion, int, error) {
	out := []SkillVersion{}
	total := 0
	if e := pageValid(p, n); e != nil {
		return out, 0, e
	}
	e := s.transaction(ctx, w, a, false, func(t SkillTx) error {
		if _, e := t.Get(ctx, id); e != nil {
			return e
		}
		var e error
		out, total, e = t.Versions(ctx, id, p, n)
		return e
	})
	return out, total, e
}
func (s *SkillService) Import(ctx context.Context, w, a string, r SkillImportRequest) (SkillImport, error) {
	ctx, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()
	var out SkillImport
	if e := s.transaction(ctx, w, a, true, func(SkillTx) error { return nil }); e != nil {
		return out, e
	}
	bundle, e := s.Assets.Prepare(ctx, r)
	if e != nil {
		return out, e
	}
	out = SkillImport{ID: NewID(), ActorID: a, TechnicalName: bundle.TechnicalName, Description: bundle.Description, Manifest: bundle.Manifest, Source: bundle.Source, StorageIdentity: s.Assets.Identity(), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	out.KeyPrefix = "temp/uploads/skill-imports/" + w + "/" + out.ID
	if e = s.transaction(ctx, w, a, true, func(t SkillTx) error { return t.SaveImport(ctx, out) }); e != nil {
		return out, e
	}
	if e = s.Assets.Write(ctx, out.KeyPrefix, bundle); e != nil {
		return out, Fail("skill_storage_unavailable", 503, "Skill 文件保存失败，未发布任何版本")
	}
	e = s.transaction(ctx, w, a, true, func(t SkillTx) error { return t.MarkReady(ctx, out.ID) })
	out.Ready = e == nil
	return out, e
}
func (s *SkillService) GetImport(ctx context.Context, w, a, id string) (SkillImport, error) {
	var out SkillImport
	e := s.transaction(ctx, w, a, true, func(t SkillTx) error {
		var e error
		out, e = t.Import(ctx, id)
		if e != nil {
			return e
		}
		return validateImport(out, a)
	})
	return out, e
}
func validateImport(v SkillImport, a string) error {
	if v.ActorID != a {
		return ErrWorkspaceMissing
	}
	if !v.Ready {
		return Fail("skill_import_incomplete", 409, "导入尚未完成，请重新导入")
	}
	if v.PublishedSkillID == "" && !time.Now().Before(v.ExpiresAt) {
		return Fail("skill_import_expired", 409, "导入已过期，请重新导入")
	}
	return nil
}
func publishFingerprint(target string, r SkillPublish) string {
	b, err := json.Marshal(struct {
		Target  string
		Request SkillPublish
	}{target, r})
	if err != nil {
		panic("unreachable: SkillPublish contains only JSON primitives")
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s *SkillService) Publish(ctx context.Context, w, a, target string, r SkillPublish) (Skill, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var out Skill
	if !agentUUID(r.ImportID) || len(r.VersionLabel) > 100 || strings.ContainsRune(r.VersionLabel, 0) {
		return out, Fail("invalid_skill", 400, "导入标识或版本标签无效")
	}
	if target == "" {
		r.Name = strings.TrimSpace(r.Name)
		if r.Name == "" || utf8.RuneCountInString(r.Name) > 100 || strings.ContainsRune(r.Name, 0) {
			return out, Fail("invalid_skill", 400, "Skill 名称无效")
		}
	} else if r.Version < 1 || r.Name != "" {
		return out, Fail("invalid_skill", 400, "更新内容版本需要当前版本号，不能同时改名")
	}
	imp, e := s.GetImport(ctx, w, a, r.ImportID)
	if e != nil {
		return out, e
	}
	fp := publishFingerprint(target, r)
	if imp.PublishedSkillID != "" {
		if imp.PublishFingerprint != fp {
			return out, Fail("skill_import_consumed", 409, "此导入已用于另一发布请求")
		}
		return s.Get(ctx, w, a, imp.PublishedSkillID)
	}
	if imp.StorageIdentity != s.Assets.Identity() {
		return out, Fail("skill_storage_changed", 409, "存储配置已变更，请迁移或重新导入")
	}
	id := target
	if id == "" {
		id = imp.ID
	}
	prefix := "workspaces/" + w + "/skills/" + id + "/versions/" + imp.ID
	e = s.transaction(ctx, w, a, true, func(t SkillTx) error {
		current, e := t.Import(ctx, imp.ID)
		if e != nil {
			return e
		}
		if e = validateImport(current, a); e != nil {
			return e
		}
		if current.PublishedSkillID != "" {
			if current.PublishFingerprint != fp {
				return Fail("skill_import_consumed", 409, "此导入已用于另一发布请求")
			}
			out, e = t.Get(ctx, current.PublishedSkillID)
			return e
		}
		sequence := int64(1)
		if target == "" {
			out = Skill{ID: id, Name: r.Name, Enabled: true, Version: 1}
		} else {
			out, e = t.Get(ctx, target)
			if e != nil {
				return e
			}
			if out.Scope == "official" {
				return Fail("official_resource_read_only", 403, "官方定义仅允许平台管理员修改")
			}
			if out.Version != r.Version {
				return Fail("skill_version_conflict", 409, "Skill 已更新，请刷新后重试")
			}
			sequence = out.CurrentVersion.Sequence + 1
			out.Version++
		}
		// Serialize publication with workspace changes before copying immutable assets.
		if e = s.Assets.Copy(ctx, imp.KeyPrefix, prefix, imp.Manifest); e != nil {
			return Fail("skill_storage_unavailable", 503, "Skill 内容校验或发布失败，版本尚不可见")
		}
		v := SkillVersion{ID: imp.ID, Sequence: sequence, VersionLabel: r.VersionLabel, TechnicalName: imp.TechnicalName, Description: imp.Description, Manifest: imp.Manifest, Source: imp.Source, KeyPrefix: prefix, StorageIdentity: imp.StorageIdentity}
		out.CurrentVersionID = v.ID
		out.Description = v.Description
		if e = t.Save(ctx, out, v, target == ""); e != nil {
			return e
		}
		if e = t.MarkPublished(ctx, imp.ID, out.ID, v.ID, fp); e != nil {
			return e
		}
		out, e = t.Get(ctx, out.ID)
		return e
	})
	return out, e
}
func (s *SkillService) Update(ctx context.Context, w, a, id string, r SkillPatch) (Skill, error) {
	var out Skill
	if r.Version < 1 || r.Name == nil && r.Enabled == nil {
		return out, Fail("invalid_skill", 400, "需要当前版本及修改字段")
	}
	if r.Name != nil {
		n := strings.TrimSpace(*r.Name)
		r.Name = &n
		if n == "" || utf8.RuneCountInString(n) > 100 || strings.ContainsRune(n, 0) {
			return out, Fail("invalid_skill", 400, "名称无效")
		}
	}
	e := s.transaction(ctx, w, a, true, func(t SkillTx) error {
		var e error
		out, e = t.Get(ctx, id)
		if e != nil {
			return e
		}
		if out.Scope == "official" {
			return Fail("official_resource_read_only", 403, "官方定义仅允许平台管理员修改")
		}
		if out.Version != r.Version {
			return Fail("skill_version_conflict", 409, "Skill 已更新，请刷新")
		}
		if r.Name != nil {
			out.Name = *r.Name
		}
		if r.Enabled != nil {
			out.Enabled = *r.Enabled
		}
		out.Version++
		if e = t.Patch(ctx, out); e != nil {
			return e
		}
		out, e = t.Get(ctx, id)
		return e
	})
	return out, e
}
func (s *SkillService) File(ctx context.Context, w, a, id, version, p string) (SkillFileContent, error) {
	var v SkillVersion
	var f SkillFile
	out := SkillFileContent{}
	e := s.transaction(ctx, w, a, false, func(t SkillTx) error {
		var e error
		v, e = t.GetVersion(ctx, id, version)
		if e != nil {
			return e
		}
		for _, entry := range v.Manifest.Files {
			if entry.Path == p {
				f = entry
				return nil
			}
		}
		return ErrWorkspaceMissing
	})
	if e != nil {
		return out, e
	}
	if v.StorageIdentity != s.Assets.Identity() {
		return out, Fail("skill_storage_changed", 503, "当前存储配置与历史资产不一致，需迁移后读取")
	}
	b, e := s.Assets.Read(ctx, v.KeyPrefix, f)
	if e != nil {
		return out, Fail("skill_asset_unavailable", 503, "文件缺失或完整性校验失败")
	}
	// Recheck membership after external storage I/O, before disclosing the content.
	if e = s.transaction(ctx, w, a, false, func(SkillTx) error { return nil }); e != nil {
		return out, e
	}
	return SkillFileContent{Path: f.Path, Size: f.Size, SHA256: f.SHA256, ContentBase64: base64.StdEncoding.EncodeToString(b)}, nil
}

// Cleanup reclaims only receipt staging objects. Published receipts remain for retry safety.
func (s *SkillService) Cleanup(ctx context.Context) error {
	candidates, e := s.Store.CleanupCandidates(ctx, s.Assets.Identity())
	if e != nil {
		return e
	}
	var errs []error
	for _, v := range candidates {
		if v.StorageIdentity != s.Assets.Identity() {
			continue
		}
		if e = s.Assets.Cleanup(ctx, v.KeyPrefix, v.Manifest); e != nil {
			errs = append(errs, e)
			continue
		}
		if e = s.Store.MarkCleaned(ctx, v.ID); e != nil {
			errs = append(errs, e)
		}
	}
	return errors.Join(errs...)
}
