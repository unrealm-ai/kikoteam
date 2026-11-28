package controller

import (
	"bytes"
	"mime"
	"net/http"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type artifactContent struct {
	file service.Artifact
	data []byte
}

func (s *Server) taskArtifact(r *http.Request, actor string) (any, int, error) {
	id, task := r.PathValue("artifact_id"), r.PathValue("task_id")
	if !service.ValidID(id) || !service.ValidID(task) {
		return nil, 400, service.Fail("invalid_argument", 400, "产物标识无效")
	}
	a, data, err := s.Service.TaskArtifact(r.Context(), workspace(r), actor, task, id)
	return artifactContent{a, data}, 200, err
}
func (d artifactContent) serve(w http.ResponseWriter, r *http.Request) {
	disposition := "attachment"
	typ := "application/octet-stream"
	if (d.file.MediaType == "image/png" || d.file.MediaType == "video/mp4") && http.DetectContentType(d.data) == d.file.MediaType {
		typ = d.file.MediaType
		if r.URL.Query().Get("download") != "1" {
			disposition = "inline"
		}
	}
	w.Header().Set("Content-Type", typ)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": d.file.Name}))
	http.ServeContent(w, r, d.file.Name, time.Time{}, bytes.NewReader(d.data))
}
