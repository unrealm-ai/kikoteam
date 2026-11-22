package service

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
)

//go:embed catalog
var catalogFiles embed.FS

type CatalogEntry struct {
	Key     string          `json:"key"`
	Listed  *bool           `json:"listed"`
	Rank    int             `json:"rank"`
	Content TemplateContent `json:"content"`
}

type TemplateCatalog struct {
	Skills   []CatalogSkill
	Plugins  []CatalogPlugin
	Revision int64
	Entries  []CatalogEntry
}

func readCatalogJSON(name string, dst any) error {
	b, err := catalogFiles.ReadFile("catalog/" + name + ".json")
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(dst); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("%s: unexpected trailing content", name)
	}
	return nil
}

func BuiltinCatalog() (TemplateCatalog, error) {
	var manifest struct {
		Revision int64
		Keys     []string
	}
	var catalog TemplateCatalog
	if err := readCatalogJSON("manifest", &manifest); err != nil {
		return catalog, err
	}
	files, err := catalogFiles.ReadDir("catalog/templates")
	if err != nil {
		return catalog, err
	}
	if manifest.Revision < 1 || len(manifest.Keys) == 0 || len(files) != len(manifest.Keys) {
		return catalog, fmt.Errorf("invalid catalog manifest or unexpected files")
	}
	refs, err := loadResources(&catalog)
	if err != nil {
		return catalog, err
	}
	catalog.Revision = manifest.Revision
	seen := map[string]bool{}
	valid := regexp.MustCompile("^[a-z][a-z0-9-]{0,63}$")
	for _, key := range manifest.Keys {
		if !valid.MatchString(key) || seen[key] || key == "manifest" {
			return catalog, fmt.Errorf("invalid catalog key %q", key)
		}
		seen[key] = true
		entry := CatalogEntry{Key: key}
		if err := readCatalogJSON("templates/"+key, &entry); err != nil {
			return catalog, err
		}
		if entry.Key != key || entry.Listed == nil || len(entry.Content.Resources) > 0 {
			return catalog, fmt.Errorf("%s: explicit listing and portable resources required", key)
		}
		if err = resolveCatalogTemplate(&entry.Content, refs); err != nil {
			return catalog, err
		}
		entry.Content = normalizeTemplate(entry.Content)
		if err := validateTemplate(entry.Content, true); err != nil {
			return catalog, fmt.Errorf("%s: %w", key, err)
		}
		catalog.Entries = append(catalog.Entries, entry)
	}
	return catalog, nil
}
