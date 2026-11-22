package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestBuiltinCatalogReleases(t *testing.T) {
	ctx := context.Background()
	db, models, w, owner := modelFixture(t)
	assets := runtimeCapabilityServices(t, db).Skills.Assets
	catalog, err := service.BuiltinCatalog()
	if err != nil {
		t.Fatal(err)
	}
	syncRelease := func() {
		t.Helper()
		if err := SyncCatalog(ctx, db, catalog, assets); err != nil {
			t.Fatal(err)
		}
	}
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- SyncCatalog(ctx, db, catalog, assets) }()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	s := service.TemplateService{Store: &TemplateStore{DB: db}}
	list := func() service.TemplatePage {
		t.Helper()
		page, err := s.List(ctx, "", owner, service.TemplateQuery{Page: 1, PageSize: 20})
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	page := list()
	if len(page.Items) != 7 {
		t.Fatal(page)
	}
	first := page.Items[0]
	if first.BuiltinKey != "business-analyst" || first.Sequence != 1 || first.Version != 1 {
		t.Fatal(first)
	}
	copy, err := s.Create(ctx, w, owner, first.Content)
	if err != nil {
		t.Fatal(err)
	}
	model := saveTestModel(t, models, w, owner)
	employee, err := s.CreateFromTemplate(ctx, w, owner, service.TemplateCreation{TemplateID: first.ID, TemplateVersionID: first.TemplateVersionID, ModelProfileID: model.ID, Name: "我的分析师"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1", owner); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Update(ctx, "", owner, first.ID, service.TemplatePatch{Version: first.Version, Content: &first.Content}); err == nil {
		t.Fatal("builtin editable")
	}
	catalog.Revision++
	catalog.Entries[0].Content.Name = "经营分析搭档"
	hidden := false
	catalog.Entries[1].Listed = &hidden
	extra := catalog.Entries[0]
	extra.Key = "new-role"
	extra.Rank = 1
	catalog.Entries = append(catalog.Entries, extra)
	catalog.Entries[2].Rank++
	syncRelease()
	syncRelease()
	if list().Total != 7 {
		t.Fatal("unexpected listing count")
	}
	current, err := s.Get(ctx, "", owner, first.ID, false)
	if err != nil || current.Sequence != 2 || current.Content.Name != "经营分析搭档" {
		t.Fatal(current, err)
	}
	copy, err = s.Get(ctx, w, owner, copy.ID, true)
	if err != nil || copy.Content.Name != first.Content.Name || copy.BuiltinKey != "" {
		t.Fatal(copy, err)
	}
	var source string
	if err = db.QueryRow("SELECT source_agent_template_version_id FROM platform_agents WHERE id=$1", employee.ID).Scan(&source); err != nil || source != first.TemplateVersionID {
		t.Fatal(source, err)
	}
	catalog.Revision++
	catalog.Entries = catalog.Entries[:6]
	catalog.Entries[0].Content.Name = "必须回滚"
	if err := SyncCatalog(ctx, db, catalog, assets); err == nil {
		t.Fatal("missing entry accepted")
	}
	current, err = s.Get(ctx, "", owner, first.ID, false)
	if err != nil || current.Sequence != 2 {
		t.Fatal(current, err)
	}
}

func TestBuiltinCatalogEmployeeBrandingUpgrade(t *testing.T) {
	ctx := context.Background()
	db, _, _, owner := modelFixture(t)
	assets := runtimeCapabilityServices(t, db).Skills.Assets
	catalog, err := service.BuiltinCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var example *service.TemplateExample
	for i := range catalog.Entries {
		entry := &catalog.Entries[i]
		if entry.Key == "content-editor" && len(entry.Content.Examples) > 0 {
			example = &entry.Content.Examples[0]
		}
	}
	if example == nil || !strings.Contains(example.Output, "AI 员工") {
		t.Fatal("missing updated official template example")
	}
	newOutput := example.Output
	example.Output = strings.Replace(newOutput, "AI 员工", "旧版称呼", 1)
	catalog.Revision--
	if err := SyncCatalog(ctx, db, catalog, assets); err != nil {
		t.Fatal(err)
	}
	catalog.Revision++
	example.Output = newOutput
	for range 2 {
		if err := SyncCatalog(ctx, db, catalog, assets); err != nil {
			t.Fatal(err)
		}
	}
	s := service.TemplateService{Store: &TemplateStore{DB: db}}
	page, err := s.List(ctx, "", owner, service.TemplateQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.BuiltinKey != "content-editor" {
			continue
		}
		if item.Sequence != 2 || len(item.Content.Examples) == 0 || item.Content.Examples[0].Output != newOutput {
			t.Fatal("updated example was not published exactly once")
		}
		return
	}
	t.Fatal("updated template missing from official catalog")
}
