package controller

import (
	"errors"
	"mime"
	"net"
	"net/http"
	"os"
	"path"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type projectDownload struct {
	File *os.File
	Info os.FileInfo
	Path string
}

func (d projectDownload) serve(w http.ResponseWriter, r *http.Request) {
	defer func() { _ = d.File.Close() }() //nolint:errcheck // Response body is streamed from a read-only descriptor.
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(d.Path)}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "application/octet-stream")
	if mt := mime.TypeByExtension(path.Ext(d.Path)); mt != "" {
		w.Header().Set("Content-Type", mt)
	}
	http.ServeContent(w, r, path.Base(d.Path), d.Info.ModTime(), d.File)
}
func (s *Server) listProjectFiles(r *http.Request, actor string) (any, int, error) {
	c, err := s.Service.WebProject(r.Context(), workspace(r), actor, r.PathValue("conversation_id"))
	if err != nil {
		return nil, 0, err
	}
	v, err := s.Service.ListProject(c, r.URL.Query().Get("path"))
	return v, http.StatusOK, err
}

func (s *Server) downloadProjectFile(r *http.Request, actor string) (any, int, error) {
	c, err := s.Service.WebProject(r.Context(), workspace(r), actor, r.PathValue("conversation_id"))
	if err != nil {
		return nil, 0, err
	}
	p := r.URL.Query().Get("path")
	f, info, err := s.Service.OpenProjectFile(c, p)
	return projectDownload{f, info, p}, http.StatusOK, err
}

func (s *Server) uploadProjectFile(r *http.Request, actor string) (any, int, error) {
	c, err := s.Service.WebProject(r.Context(), workspace(r), actor, r.PathValue("conversation_id"))
	if err != nil {
		return nil, 0, err
	}
	if err := parseProjectMultipart(r); err != nil {
		return nil, 0, err
	}
	defer func() {
		if err := r.MultipartForm.RemoveAll(); err != nil && s.Logger != nil {
			s.Logger.Warn("Multipart temporary file cleanup failed")
		}
	}()
	files := r.MultipartForm.File["file"]
	if len(files) != 1 || len(r.MultipartForm.File) != 1 {
		return nil, 0, service.Fail("invalid_argument", 400, "请选择一个文件")
	}
	directory := "uploads"
	for field, values := range r.MultipartForm.Value {
		if field != "directory" || len(values) != 1 {
			return nil, 0, service.Fail("invalid_argument", 400, "目录参数无效")
		}
		directory = values[0]
	}
	f, err := files[0].Open()
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }() //nolint:errcheck // Multipart input is read-only.
	v, replay, err := s.Service.UploadProject(r.Context(), c, actor, r.Header.Get("Idempotency-Key"), directory, files[0].Filename, f)
	status := 201
	if replay {
		status = 200
	}
	return receipt{Value: v, Replay: replay}, status, err
}

func parseProjectMultipart(r *http.Request) error {
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return service.Fail("upload_timeout", http.StatusRequestTimeout, "上传超时，请重试")
		}
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			return service.Fail("file_too_large", 413, "文件超过大小限制")
		}
		return service.Fail("invalid_argument", 400, "需要 multipart 文件上传")
	}
	return nil
}
