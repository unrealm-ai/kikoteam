package service

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type HumanRecipient struct {
	ID        string                `json:"id,omitempty"`
	Name      string                `json:"name,omitempty"`
	Scope     string                `json:"scope"`
	IsDefault bool                  `json:"is_default,omitempty"`
	Ref       platform.SelectionRef `json:"ref"`
}

type HumanRecipients struct {
	Version    int64            `json:"version,string"`
	Recipients []HumanRecipient `json:"recipients"`
}

type HumanRuntimeScope struct {
	CorrectionRequestID                                                 string
	Identity                                                            DelegationIdentity
	InputID, Workspace, Agent, Turn, Mode, User, ChannelPrincipal, Text string
}

type HumanListQuery struct {
	RequestID string `json:"requestId,omitempty"`
	Page      *int   `json:"page,omitempty"`
	PageSize  *int   `json:"pageSize,omitempty"`
}

func (q HumanListQuery) Pagination() (page, size int, err error) {
	page, size = 1, 20
	if q.Page != nil {
		page = *q.Page
	}
	if q.PageSize != nil {
		size = *q.PageSize
	}
	if page < 1 || size < 1 || size > 50 || page-1 > math.MaxInt/size || len(q.RequestID) > 36 ||
		(q.RequestID != "" && !IsHumanCode(q.RequestID)) {
		err = Fail("invalid_human_list_page", 400, "页码必须为正整数，每页 1 到 50 条，编号须为完整 ID 或 H- 短码")
	}
	return
}

type HumanQuestion struct {
	CandidateID        string              `json:"candidateId,omitempty"`
	QuestionScope      string              `json:"questionScope,omitempty"`
	DecisionRequired   *bool               `json:"decisionRequired,omitempty"`
	Goal               string              `json:"goal,omitempty"`
	KnownFacts         []HumanFact         `json:"knownFacts"`
	Questions          []HumanQuestionItem `json:"questions,omitempty"`
	CompletionCriteria []string            `json:"completionCriteria,omitempty"`
	ProposedAction     string              `json:"proposedAction,omitempty"`
	ImpactScope        string              `json:"impactScope,omitempty"`
	IfNotConfirmed     string              `json:"ifNotConfirmed,omitempty"`
	RecipientID        string              `json:"recipient_id"`
	RulesVersion       int64               `json:"rules_version,string"`
	Question           string              `json:"question"`
	Background         string              `json:"background,omitempty"`
	AnswerType         string              `json:"answer_type"`
}

type HumanFact struct {
	Text         string   `json:"text"`
	EvidenceRefs []string `json:"evidenceRefs,omitempty"`
}

type HumanQuestionItem struct {
	ID          string                `json:"id"`
	Text        string                `json:"text"`
	Required    *bool                 `json:"required"`
	AnswerType  string                `json:"answerType"`
	Constraints []string              `json:"constraints,omitempty"`
	Options     []HumanQuestionOption `json:"options,omitempty"`
}

type HumanQuestionOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type HumanAnswerItem struct {
	QuestionID string   `json:"questionId"`
	Value      string   `json:"value,omitempty"`
	OptionIDs  []string `json:"optionIds,omitempty"`
}

type HumanAnswer struct {
	Answers            []HumanAnswerItem `json:"answers,omitempty"`
	QuestionDigest     string            `json:"questionDigest,omitempty"`
	Association        string            `json:"association,omitempty"`
	EvidenceSegmentIDs []string          `json:"evidenceSegmentIds,omitempty"`
	ClarificationText  string            `json:"clarificationText,omitempty"`

	Version  int64  `json:"version,string"`
	Decision string `json:"decision"`
	Text     string `json:"text,omitempty"`
}

type HumanRequest struct {
	ID             string        `json:"id"`
	Version        int64         `json:"version,string"`
	State          string        `json:"state"`
	Question       HumanQuestion `json:"question"`
	Fenced         bool          `json:"fenced,omitempty"`
	Code           string        `json:"code,omitempty"`
	QuestionDigest string        `json:"questionDigest,omitempty"`
}

type HumanHistoryQuestion struct {
	Questions      []HumanQuestionItem `json:"questions,omitempty"`
	ProposedAction string              `json:"proposedAction,omitempty"`
	ImpactScope    string              `json:"impactScope,omitempty"`
	Question       string              `json:"question"`
	Background     string              `json:"background,omitempty"`
	AnswerType     string              `json:"answer_type"`
}

type HumanHistoryItem struct {
	ID                string                `json:"id"`
	Version           int64                 `json:"version,string"`
	State             string                `json:"state"`
	DetailVisible     bool                  `json:"detail_visible"`
	Question          *HumanHistoryQuestion `json:"question,omitempty"`
	RecipientName     *string               `json:"recipient_name,omitempty"`
	RecipientProvider *string               `json:"recipient_provider,omitempty"`
	AgentName         *string               `json:"agent_name,omitempty"`
	CreatedAt         time.Time             `json:"created_at"`
	AnsweredAt        *time.Time            `json:"answered_at,omitempty"`
	Answer            *HumanAnswer          `json:"answer,omitempty"`
	Clarification     string                `json:"clarification,omitempty"`
}
type HumanHistoryPage struct {
	Items    []HumanHistoryItem `json:"items"`
	Total    int                `json:"total"`
	Pending  int                `json:"pending"`
	Resolved int                `json:"resolved"`
}

func (q *HumanQuestion) Validate() error {
	if q.CandidateID != "" || q.Questions != nil {
		if err := q.normalizeStructured(); err != nil {
			return err
		}
	}
	q.Question, q.Background = strings.TrimSpace(q.Question), strings.TrimSpace(q.Background)
	limit := 2000
	if len(q.Questions) != 0 {
		limit = 12000
	}
	if !ValidID(q.RecipientID) || q.RulesVersion < 1 || !humanText(q.Question, limit) ||
		(q.Background != "" && !humanText(q.Background, 3000)) || (q.AnswerType != "decision" && q.AnswerType != "information") {
		return Fail("invalid_human_question", 400, "请选择当前确认人并填写有效问题")
	}
	return nil
}

func (a *HumanAnswer) Validate(kind string) error {
	a.Text = strings.TrimSpace(a.Text)
	if a.Version < 1 || (a.Text != "" && !humanText(a.Text, 4000)) {
		return Fail("invalid_human_answer", 400, "答复内容或版本无效")
	}
	switch a.Decision {
	case "reject":
		return nil
	case "revise", "needs_more":
		if (kind == "decision" || kind == "information") && a.Text != "" {
			return nil
		}
	case "approve":
		if kind == "decision" {
			return nil
		}
	case "information":
		if kind == "information" && (a.Text != "" || len(a.Answers) != 0) {
			return nil
		}
	}
	return Fail("invalid_human_answer", 400, "答复与问题类型不匹配")
}

func humanText(value string, maxLength int) bool {
	return value != "" && utf8.RuneCountInString(value) <= maxLength && !strings.ContainsRune(value, 0)
}

func humanRuntimeSecret(secret, input string) string {
	sum := sha256.Sum256([]byte("human-runtime:" + secret + ":" + input))
	return hex.EncodeToString(sum[:])
}

func SignHumanRuntime(secret, input string, v DelegationIdentity) (string, error) {
	return SignDelegation(humanRuntimeSecret(secret, input), v)
}

func VerifyHumanRuntime(secret, input, token string) (DelegationIdentity, error) {
	if !ValidID(input) || len(secret) < 32 {
		return DelegationIdentity{}, Fail("unauthenticated", 401, "确认工具执行身份无效")
	}
	return VerifyDelegation(humanRuntimeSecret(secret, input), token)
}

func SignHumanMCP(secret, mode, input string, v DelegationIdentity) (string, error) {
	return SignHumanRuntime("human-mcp:"+mode+":"+secret, input, v)
}

func VerifyHumanMCP(secret, mode, input, token string) (DelegationIdentity, error) {
	if len(secret) < 32 {
		return DelegationIdentity{}, Fail("unauthenticated", 401, "确认工具执行身份无效")
	}
	return VerifyHumanRuntime("human-mcp:"+mode+":"+secret, input, token)
}
