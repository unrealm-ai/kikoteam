package service

import (
	"context"
	"strings"
	"unicode/utf8"
)

const WorkInputHeader = "X-Kiko-Work-Input"

type WorkScope struct {
	Identity DelegationIdentity
	InputID  string
}

type WorkToolStore interface {
	WorkTool(context.Context, WorkScope, string, WorkArguments) (any, error)
}

type WorkAssets interface {
	PutWorkFile(context.Context, string, []byte) (string, error)
	GetWorkFile(context.Context, string, string, int64) ([]byte, error)
}
type WorkArguments struct {
	Path      string   `json:"path,omitempty"`
	FilePaths []string `json:"file_paths,omitempty"`
	FileIDs   []string `json:"file_ids,omitempty"`
	FileID    string   `json:"file_id,omitempty"`
	Offset    int64    `json:"offset,omitempty"`

	OperationID          string `json:"operation_id,omitempty"`
	BoundaryID           string `json:"boundary_id,omitempty"`
	TargetEmployeeID     string `json:"target_employee_id,omitempty"`
	ExpectedAssignmentID string `json:"expected_assignment_id,omitempty"`
	SubmissionID         string `json:"submission_id,omitempty"`
	ExpectedVersion      int64  `json:"expected_version,string,omitempty"`
	Title                string `json:"title,omitempty"`
	Goal                 string `json:"goal,omitempty"`
	Criteria             string `json:"criteria,omitempty"`
	Summary              string `json:"summary,omitempty"`
	Result               string `json:"result,omitempty"`
	Evidence             string `json:"completion_evidence,omitempty"`
	Query                string `json:"q,omitempty"`
}

func SignWorkTool(secret, audience, input string, identity DelegationIdentity) (string, error) {
	return SignHumanRuntime("work-item:"+audience+":"+secret, input, identity)
}

func VerifyWorkTool(secret, audience, input, token string) (DelegationIdentity, error) {
	if len(secret) < 32 {
		return DelegationIdentity{}, Fail("unauthenticated", 401, "事项工具执行身份无效")
	}
	return VerifyHumanRuntime("work-item:"+audience+":"+secret, input, token)
}

func (a WorkArguments) ValidateAction(op, role string) error {
	if a.validAction(op, role) {
		return nil
	}
	return Fail("invalid_work_arguments", 400, "事项工具参数或当前任务角色无效")
}

func (a WorkArguments) validAction(op, role string) bool {
	if a.BoundaryID != "" || a.Query != "" || a.FileID != "" || a.Offset != 0 || len(a.FilePaths)+len(a.FileIDs) > 32 || op == "accept" && len(a.FilePaths)+len(a.FileIDs) > 0 {
		return false
	}
	firstFields := a.Title != "" || a.Goal != "" || a.Criteria != ""
	switch op {
	case "submit":
		return role == "assignee" && a.TargetEmployeeID == "" && a.SubmissionID == "" && a.Summary == "" && !firstFields &&
			ValidID(a.ExpectedAssignmentID) && a.ExpectedVersion > 0 && workText(a.Result, 10000) && workText(a.Evidence, 4000)
	case "accept":
		return role == "review" && a.OperationID == "" && a.TargetEmployeeID == "" && a.ExpectedAssignmentID == "" && a.Summary == "" && !firstFields &&
			a.Result == "" && ValidID(a.SubmissionID) && a.ExpectedVersion > 0 && workText(a.Evidence, 4000)
	case "handoff":
		return ValidID(a.TargetEmployeeID) && workText(a.Summary, 4000) && a.Result == "" && a.Evidence == "" && a.validHandoffRole(role, firstFields)
	default:
		return false
	}
}

func (a WorkArguments) validHandoffRole(role string, firstFields bool) bool {
	switch role {
	case "ordinary":
		return a.ExpectedAssignmentID == "" && a.SubmissionID == "" && a.ExpectedVersion == 0 &&
			workText(a.Title, 200) && workText(a.Goal, 4000) && workText(a.Criteria, 4000)
	case "assignee":
		return !firstFields && a.SubmissionID == "" && ValidID(a.ExpectedAssignmentID) && a.ExpectedVersion > 0
	case "review":
		return !firstFields && a.ExpectedAssignmentID == "" && ValidID(a.SubmissionID) && a.ExpectedVersion > 0
	default:
		return false
	}
}

func workText(s string, max int) bool {
	return strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= max
}
