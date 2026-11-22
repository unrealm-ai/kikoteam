package controller

import (
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"net/http"
)

func (s *Server) taskResult(r *http.Request) (any, int, error) {
	var req service.TaskResult
	if err := decode(r, &req); err != nil {
		return nil, 400, err
	}
	store, ok := s.Service.Store.(service.TaskReportStore)
	if !ok {
		return nil, 503, service.Fail("dependency_unavailable", 503, "任务回报未接入")
	}
	out, err := (service.TaskReports{Store: store}).Report(r.Context(), req)
	return out, 200, err
}
func (s *ChannelServer) reconcileReports(r *http.Request) (any, error) {
	var req struct {
		Limit int `json:"limit"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	store, ok := s.Manager.Store.(service.TaskReportStore)
	if !ok {
		return nil, service.Fail("dependency_unavailable", 503, "任务回报未接入")
	}
	count, err := (service.TaskReports{Store: store}).Reconcile(r.Context(), req.Limit)
	return map[string]int{"processed": count}, err
}
