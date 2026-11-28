package controller

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type workspaceSearchItem struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	AgentID  string `json:"agent_id,omitempty"`
}
type workspaceSearchResult struct {
	Query       string                `json:"query"`
	Items       []workspaceSearchItem `json:"items"`
	Unavailable []string              `json:"unavailable,omitempty"`
}

func (s *Server) workspaceSearch(r *http.Request, actor string) (any, int, error) {
	if err := taskQuery(r, "q"); err != nil {
		return nil, 400, err
	}
	w, q := workspace(r), strings.TrimSpace(r.URL.Query().Get("q"))
	if !service.ValidID(w) || !utf8.ValidString(q) || strings.ContainsAny(q, "\x00\r\n") || q == "" || utf8.RuneCountInString(q) > 120 {
		return nil, 400, service.Fail("invalid_argument", 400, "搜索词须为 1–120 个字符")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	const limit = 5
	// Employee listing validates active membership before other sources are queried
	// and returns only the information this actor is allowed to see.
	agents, err := s.SearchAgents.List(ctx, w, actor, platform.AgentListQuery{Search: q, Page: 1, PageSize: limit})
	if err != nil {
		return nil, 0, err
	}
	out := workspaceSearchResult{Query: q, Items: []workspaceSearchItem{}}
	for _, a := range agents.Items {
		out.Items = append(out.Items, workspaceSearchItem{ID: a.ID, Kind: "employee", Title: a.Name, Subtitle: searchSubtitle(a.Role)})
	}
	tasks, err := s.Service.ListTasks(ctx, w, actor, "", service.TaskFilter{Q: q, Limit: limit})
	if err != nil {
		out.Unavailable = append(out.Unavailable, "task")
	} else {
		for _, task := range tasks.Items {
			title := "未命名任务"
			if task.Title != nil && *task.Title != "" {
				title = *task.Title
			}
			out.Items = append(out.Items, workspaceSearchItem{ID: task.TaskID, Kind: "task", Title: title, Subtitle: task.Agent.Name})
		}
	}
	chats, err := s.Service.SearchWebConversations(ctx, w, actor, q, limit)
	if err != nil {
		out.Unavailable = append(out.Unavailable, "conversation")
	} else {
		for _, chat := range chats {
			out.Items = append(out.Items, workspaceSearchItem{ID: chat.ID, Kind: "conversation", Title: chat.Title, Subtitle: chat.AgentName, AgentID: chat.AgentID})
		}
	}
	return out, 200, nil
}

func searchSubtitle(text string) string {
	value := []rune(strings.TrimSpace(text))
	if len(value) > 96 {
		return string(value[:96]) + "…"
	}
	return string(value)
}
