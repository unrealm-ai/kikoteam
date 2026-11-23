package controller

import (
	"context"
	"net/http"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func (s *Server) webAttachmentRoutes(mux *http.ServeMux, base string) {
	mux.HandleFunc("POST "+base, s.wrap("reserveWebAttachment", s.reserveWebAttachment))
	mux.HandleFunc("GET "+base+"/{attachment_id}", s.wrap("getWebAttachment", s.getWebAttachment))
	mux.HandleFunc("POST "+base+"/{attachment_id}/upload", s.wrap("uploadReservedAttachment", s.uploadReservedAttachment))
}

func (s *Server) reserveWebAttachment(r *http.Request, actor string) (any, int, error) {
	var req struct {
		Name string `json:"name"`
		Size *int64 `json:"size"`
	}
	if err := decode(r, &req); err != nil {
		return nil, 0, err
	}
	if req.Size == nil {
		return nil, 0, service.Fail("invalid_argument", 400, "需要附件大小")
	}
	out, replay, err := s.Service.ReserveWebAttachment(r.Context(), workspace(r), actor, r.PathValue("conversation_id"), r.Header.Get("Idempotency-Key"), req.Name, *req.Size)
	if err != nil {
		return nil, 0, err
	}
	status := http.StatusCreated
	if replay {
		status = http.StatusOK
	}
	return receipt{Value: out, Replay: replay, Location: r.URL.Path + "/" + out.ID}, status, nil
}

func (s *Server) getWebAttachment(r *http.Request, actor string) (any, int, error) {
	out, err := s.Service.WebAttachment(r.Context(), workspace(r), actor, r.PathValue("conversation_id"), r.PathValue("attachment_id"))
	return out, http.StatusOK, err
}

func (s *Server) uploadReservedAttachment(r *http.Request, actor string) (any, int, error) {
	u, err := s.Service.BeginWebAttachment(r.Context(), workspace(r), actor, r.PathValue("conversation_id"), r.PathValue("attachment_id"))
	if err != nil {
		return nil, 0, err
	}
	if u.Status == "ready" {
		return receipt{Value: u.Attachment, Replay: true}, http.StatusOK, nil
	}
	out, err := s.publishReservedMultipart(r, u)
	if err != nil {
		// A disconnected browser (and the request deadline) cannot prevent publication
		// of an authenticated failure. An interrupted process expires the reservation.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		if e := s.Service.FailWebAttachment(ctx, u); e != nil && s.Logger != nil {
			s.Logger.Warn("Failed to finalize Web attachment failure", "attachment_id", u.ID)
		}
		return nil, 0, err
	}
	return out, http.StatusCreated, nil
}

func (s *Server) publishReservedMultipart(r *http.Request, u service.WebAttachmentUpload) (service.Attachment, error) {
	if err := parseProjectMultipart(r); err != nil {
		return service.Attachment{}, err
	}
	defer func() {
		if e := r.MultipartForm.RemoveAll(); e != nil && s.Logger != nil {
			s.Logger.Warn("Multipart temporary file cleanup failed")
		}
	}()
	files := r.MultipartForm.File["file"]
	if len(files) != 1 || len(r.MultipartForm.File) != 1 || len(r.MultipartForm.Value) != 0 {
		return service.Attachment{}, service.Fail("invalid_argument", 400, "请选择一个文件")
	}
	f, err := files[0].Open()
	if err != nil {
		return service.Attachment{}, err
	}
	defer func() { _ = f.Close() }() //nolint:errcheck // Multipart input is read-only.
	return s.Service.PublishWebAttachment(r.Context(), u, files[0].Filename, files[0].Size, f)
}
