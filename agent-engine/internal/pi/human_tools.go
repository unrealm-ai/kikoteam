package pi

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type HumanToolsConfig struct {
	TaskSpaceHistoryURL     string `json:"task_space_history_url,omitempty"`
	TaskSpaceHistoryToken   string `json:"task_space_history_token,omitempty"`
	RecipientsMCPURL        string `json:"recipients_mcp_url,omitempty"`
	RecipientsMCPToken      string `json:"recipients_mcp_token,omitempty"`
	AssistantMCPBaseURL     string `json:"assistant_mcp_base_url,omitempty"`
	AssistantDemoToken      string `json:"assistant_demo_token,omitempty"`
	EmployeeManagementToken string `json:"employee_management_token,omitempty"`
	WorkMCPURL              string `json:"work_mcp_url,omitempty"`
	WorkMCPToken            string `json:"work_mcp_token,omitempty"`
	WorkURL                 string `json:"work_url,omitempty"`
	WorkToken               string `json:"work_token,omitempty"`
	WorkRole                string `json:"work_role,omitempty"`
	URL                     string `json:"url"`
	Token                   string `json:"token"`
	Input                   string `json:"input"`
	Mode                    string `json:"mode"`
	RecipientsMCP           bool   `json:"recipients_mcp,omitempty"`
	ReapprovalOnly          bool   `json:"reapproval_only,omitempty"`
}

func (h HumanToolsConfig) validForPlan(enabled bool) bool {
	if !enabled {
		return h == (HumanToolsConfig{})
	}
	return h.ReapprovalOnly && h.Mode == "task" && h.URL != "" && h.Token != "" && h.Input != "" && !h.RecipientsMCP &&
		h.RecipientsMCPURL == "" && h.RecipientsMCPToken == "" &&
		h.TaskSpaceHistoryURL == "" && h.TaskSpaceHistoryToken == "" &&
		h.WorkMCPURL == "" && h.WorkMCPToken == "" && h.WorkURL == "" && h.WorkToken == "" && h.WorkRole == "" &&
		h.AssistantMCPBaseURL == "" && h.AssistantDemoToken == "" && h.EmployeeManagementToken == ""
}

func (c Config) humanToolsPath() string {
	return filepath.Join(c.RuntimeDirectory, "human-tools.json")
}

// Turn-bound credentials are excluded from the Pi configuration signature.
// Publish them atomically so a reused process never sees a partial file.
// The extension refreshes its native transport only between settled prompts.
func (c Config) PrepareHumanTools() error {
	if c.HumanToolsExtension == "" {
		return nil
	}
	data, err := json.Marshal(struct {
		HumanToolsConfig
		RelayServers map[string]RelayServer       `json:"relay_servers,omitempty"`
		RelayHeaders map[string]map[string]string `json:"relay_headers,omitempty"`
	}{c.HumanTools, c.RelayServers, c.RelayHeaders})
	if err != nil {
		return err
	}
	if err = os.MkdirAll(c.RuntimeDirectory, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(c.RuntimeDirectory, ".human-tools-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }() //nolint:errcheck // Best-effort cleanup after write/rename.
	_, err = file.Write(data)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), c.humanToolsPath())
}
