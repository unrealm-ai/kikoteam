package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type humanEvidenceSegment struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}
type humanReplyRef struct {
	RequestID string `json:"request_id"`
	Version   int64  `json:"version,string"`
	Digest    string `json:"questionDigest,omitempty"`
}
type humanReplyContext struct {
	ID                 string                 `json:"id"`
	Text               string                 `json:"text"`
	Segments           []humanEvidenceSegment `json:"segments"`
	QuotePresent       bool                   `json:"quotePresent,omitempty"`
	QuotedRequests     []humanReplyRef        `json:"quotedRequests,omitempty"`
	ClarificationFocus []humanReplyRef        `json:"clarificationFocus,omitempty"`
}

var humanEvidenceParts = regexp.MustCompile(`[^，,。！？!?;；\n]+[，,。！？!?;；\n]?`)
var humanCodeMarker = regexp.MustCompile(`(?i)\b(?:H-[0-9a-f]{12}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\b`)

func humanSegments(input, text string) []humanEvidenceSegment {
	result := []humanEvidenceSegment{}
	for _, span := range humanEvidenceParts.FindAllStringIndex(text, -1) {
		if body := strings.TrimSpace(text[span[0]:span[1]]); body != "" {
			sum := sha256.Sum256([]byte(input + ":" + strconv.Itoa(span[0]) + ":" + body))
			result = append(result, humanEvidenceSegment{ID: hex.EncodeToString(sum[:12]), Text: body})
		}
	}
	return result
}

func humanInboundEvidence(ctx context.Context, tx *sql.Tx, viewer humanViewer) (string, string, string, error) {
	if !viewer.channel {
		return "", "", "", nil
	}
	var id, quote, text string
	err := tx.QueryRowContext(ctx, `SELECT n.id,COALESCE(n.message_metadata->>'reply_message_id',''),n.text
 FROM manager_channel_inbox n WHERE n.input_id=$1 AND manager_human_channel_inbox_current(n.id,$2)`, viewer.input, viewer.actor).Scan(&id, &quote, &text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", nil
	}
	return id, quote, text, err
}

func readHumanReplyContext(ctx context.Context, tx *sql.Tx, viewer humanViewer, original string) (humanReplyContext, error) {
	inbox, quote, raw, err := humanInboundEvidence(ctx, tx, viewer)
	if viewer.channel {
		original = raw
	}
	out := humanReplyContext{ID: viewer.input, Text: original, Segments: humanSegments(viewer.input, original)}
	if err != nil {
		return out, err
	}
	out.QuotePresent = quote != ""
	out.ClarificationFocus, err = visibleHumanClarification(ctx, tx, viewer, inbox, "")
	if err != nil {
		return out, err
	}
	if out.QuotePresent {
		out.QuotedRequests, err = visibleHumanClarification(ctx, tx, viewer, inbox, quote)
		if err != nil || len(out.QuotedRequests) != 0 {
			return out, err
		}
		quoteViewer := viewer
		quoteViewer.input, quoteViewer.inbox = "", inbox
		selected, err := findHumanDMSent(ctx, tx, quoteViewer, quote)
		if err == nil && selected.id != "" {
			out.QuotedRequests = []humanReplyRef{{RequestID: selected.id, Version: selected.version}}
		}
		return out, err
	}
	return out, nil
}

// References may point past the current page. They remain subject to the same
// recipient and current-request checks as the paginated items.
func (r humanReplyContext) referencedLocators() []string {
	ids := make([]string, 0, 20)
	seen := make(map[string]bool)
	appendID := func(id string) {
		id = strings.ToLower(id)
		if len(ids) < 20 && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	for _, refs := range [][]humanReplyRef{r.QuotedRequests, r.ClarificationFocus} {
		for _, ref := range refs {
			appendID(ref.RequestID)
		}
	}
	for _, id := range humanCodeMarker.FindAllString(r.Text, -1) {
		appendID(id)
	}
	return ids
}

func visibleHumanClarification(ctx context.Context, tx *sql.Tx, viewer humanViewer, inbox, quote string) ([]humanReplyRef, error) {
	var input, rendered string
	var err error
	if viewer.channel {
		if inbox == "" {
			return nil, nil
		}
		err = tx.QueryRowContext(ctx, `SELECT old.input_id,d.text
 FROM manager_channel_inbox incoming
 JOIN manager_channel_inbox old ON old.conversation_id=incoming.conversation_id AND old.principal_id=incoming.principal_id
 JOIN manager_channel_deliveries d ON d.inbox_id=old.id AND d.binding_id=incoming.binding_id
 JOIN manager_turns t ON t.input_id=old.input_id AND t.status='completed'
 WHERE incoming.id=$1 AND d.status='sent' AND d.kind='reply' AND d.connection_generation=incoming.connection_generation
 AND old.identity_epoch=incoming.identity_epoch AND old.config_revision=incoming.config_revision
 AND d.confirmed_at<=incoming.created_at
 AND ($2='' OR (d.provider='feishu' AND d.provider_message_id=$2))
 ORDER BY d.confirmed_at DESC,d.id DESC LIMIT 1`, inbox, quote).Scan(&input, &rendered)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT t.input_id, e.event::text
 FROM manager_inputs incoming
 JOIN manager_turns t ON t.session_id=incoming.session_id AND t.status='completed'
 JOIN manager_inputs old ON old.id=t.input_id AND old.submitted_by=incoming.submitted_by
 JOIN manager_session_events e ON e.session_id=t.session_id AND e.event->>'input_id'=t.input_id::text
 AND e.event->>'type'='agent.message'
 WHERE incoming.id=$1 AND t.finished_at<=incoming.created_at
 ORDER BY e.seq DESC LIMIT 1`, viewer.input).Scan(&input, &rendered)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !viewer.channel {
		rendered, err = humanWebReplyText(rendered)
		if err != nil {
			return nil, err
		}
	}
	return matchingHumanClarifications(ctx, tx, viewer, input, rendered)
}

func humanWebReplyText(raw string) (string, error) {
	var event struct {
		Content []struct {
			Type string
			Text string
		}
	}
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		return "", err
	}
	var parts []string
	for _, part := range event.Content {
		if part.Type == "text" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

func matchingHumanClarifications(ctx context.Context, tx *sql.Tx, viewer humanViewer, input, visible string) ([]humanReplyRef, error) {
	rows, err := tx.QueryContext(ctx, `SELECT c.request_id,c.question_version,c.question_digest,c.body
 FROM manager_human_assistant_clarifications c JOIN manager_human_requests r ON r.id=c.request_id
 WHERE `+humanAnswerTarget+` AND c.input_id=$7 ORDER BY c.request_id`, append(viewer.queryArgs(), input)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit closes report errors below.
	var out []humanReplyRef
	for rows.Next() {
		var ref humanReplyRef
		var body string
		if err = rows.Scan(&ref.RequestID, &ref.Version, &ref.Digest, &body); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		if strings.Contains(strings.Join(strings.Fields(visible), " "), strings.Join(strings.Fields(body), " ")) {
			out = append(out, ref)
		}
	}
	return out, errors.Join(rows.Err(), rows.Close())
}

func humanSelectedEvidence(reply humanReplyContext, selected []string) (string, error) {
	if len(selected) == 0 {
		return reply.Text, nil
	}
	valid := make(map[string]bool, len(selected))
	for _, id := range selected {
		if valid[id] {
			return "", service.Fail("human_evidence_invalid", 409, "答复片段重复，请核对当前消息")
		}
		valid[id] = true
	}
	var parts []string
	for _, part := range reply.Segments {
		if valid[part.ID] {
			parts = append(parts, part.Text)
			delete(valid, part.ID)
		}
	}
	if len(valid) != 0 {
		return "", service.Fail("human_evidence_invalid", 409, "答复片段不属于当前真实消息")
	}
	return strings.Join(parts, " "), nil
}

func humanReferenceMatches(ref humanReplyRef, request service.HumanRequest) bool {
	return ref.RequestID == request.ID && ref.Version == request.Version &&
		(ref.Digest == "" || ref.Digest == request.QuestionDigest)
}

func verifyHumanReplyAssociation(reply humanReplyContext, request service.HumanRequest, answer service.HumanAnswer, selected string) error {
	markers := humanCodeMarker.FindAllString(selected, -1)
	for _, marker := range markers {
		if strings.EqualFold(marker, request.Code) || (request.Version == 1 && strings.EqualFold(marker, request.ID)) {
			continue
		}
		return service.Fail("human_request_mismatch", 409, "当前答复片段中的编号与提交的确认题不同")
	}
	focus := reply.ClarificationFocus
	if reply.QuotePresent {
		focus = reply.QuotedRequests
		for _, ref := range focus {
			if ref.RequestID == request.ID && !humanReferenceMatches(ref, request) {
				return service.Fail("human_request_changed", 409, "引用的问题版本已变化，请重新查看")
			}
		}
		if len(focus) == 1 && focus[0].RequestID != request.ID &&
			(len(markers) == 0 || !humanMessageMentions(reply.Text, focus[0])) {
			return service.Fail("human_request_mismatch", 409, "当前引用与提交的确认题不一致，请核对答复范围")
		}
	}
	switch answer.Association {
	case "current_message":
		return nil
	case "clarification":
		if len(focus) == 1 && humanReferenceMatches(focus[0], request) {
			return nil
		}
	case "unclear":
		if answer.Decision == "needs_more" {
			return nil
		}
	}
	return service.Fail("human_request_ambiguous", 409, "请核对当前题目，或先澄清用户指哪一项")
}

func humanMessageMentions(text string, ref humanReplyRef) bool {
	for _, marker := range humanCodeMarker.FindAllString(text, -1) {
		if strings.EqualFold(marker, service.HumanShortCode(ref.RequestID, ref.Version)) ||
			(ref.Version == 1 && strings.EqualFold(marker, ref.RequestID)) {
			return true
		}
	}
	return false
}

func proposeHumanReply(ctx context.Context, tx *sql.Tx, viewer humanViewer, scope service.HumanRuntimeScope, id string, answer service.HumanAnswer, values []service.HumanModelAnswerItem) (any, error) {
	request, err := scanHumanRequest(tx.QueryRowContext(ctx, `SELECT r.id,r.version,r.state,r.question,r.fenced_at IS NOT NULL
 FROM manager_human_requests r JOIN manager_inputs i ON i.id=$5 AND i.created_at>=r.created_at
 WHERE r.id=$7 AND `+humanAnswerTarget, append(viewer.queryArgs(), id)...))
	if err != nil {
		return nil, visibleError(err)
	}
	if request.Version != answer.Version || request.QuestionDigest != answer.QuestionDigest {
		return nil, service.Fail("human_request_changed", 409, "问题已变化，请重新查看")
	}
	reply, err := readHumanReplyContext(ctx, tx, viewer, scope.Text)
	if err != nil {
		return nil, err
	}
	selected, err := humanSelectedEvidence(reply, answer.EvidenceSegmentIDs)
	if err != nil {
		return nil, err
	}
	if err = verifyHumanReplyAssociation(reply, request, answer, selected); err != nil {
		return nil, err
	}
	if answer.Decision == "needs_more" {
		body := strings.TrimSpace(answer.ClarificationText)
		if request.State != "pending" || answer.Association == "unclear" || body == "" || len([]rune(body)) > 2000 || strings.ContainsRune(body, 0) {
			return nil, service.Fail("human_clarification_invalid", 409, "先定位有效问题，再登记要实际发出的完整追问")
		}
		var stored string
		err = tx.QueryRowContext(ctx, `INSERT INTO manager_human_assistant_clarifications(input_id,request_id,question_version,question_digest,body)
 VALUES($1,$2,$3,$4,$5) ON CONFLICT(input_id,request_id,question_version) DO UPDATE SET body=EXCLUDED.body
 WHERE manager_human_assistant_clarifications.body=EXCLUDED.body RETURNING body`, viewer.input, id, request.Version, request.QuestionDigest, body).Scan(&stored)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.Fail("human_clarification_conflict", 409, "此轮已登记了另一句追问")
		}
		return map[string]any{"state": "needs_more", "id": id, "clarificationText": stored}, err
	}
	if answer.ClarificationText != "" || strings.TrimSpace(selected) == "" {
		return nil, service.Fail("human_evidence_invalid", 409, "正式提交需要本次真实回复")
	}
	if len(values) != 0 {
		answer.Answers, err = request.Question.ModelAnswers(values)
		if err != nil {
			return nil, err
		}
	}
	answer.Association, answer.QuestionDigest, answer.EvidenceSegmentIDs = "", "", nil
	return recordHumanAnswer(ctx, tx, viewer, id, answer)
}
