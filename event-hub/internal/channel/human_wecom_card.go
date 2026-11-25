package channel

import (
	"encoding/json"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	wecomHumanButton   = "button_interaction"
	wecomHumanVote     = "vote_interaction"
	wecomHumanDropdown = "multiple_interaction"

	wecomHumanSingleKey    = "kiko_submit_choice"
	wecomHumanMultiKey     = "kiko_submit_multi_choice"
	wecomHumanDropdownKey  = "kiko_submit_dropdowns"
	wecomHumanChoiceEmpty  = "kiko_unselected"
	wecomHumanUpdateAction = "https://work.weixin.qq.com/"
)

// renderWecomHumanCard returns provider fields only for a complete, representable
// confirmation. In particular, never truncate or silently skip material context,
// questions, or options to fit the provider's smaller native card.
func renderWecomHumanCard(card *humanConfirmationCard, taskID string) (map[string]any, bool) {
	if card == nil || !validWecomHumanKey(taskID, 128, true) || !validWecomHumanText(card.Title, 26, false) {
		return nil, false
	}
	title := map[string]string{"title": card.Title}
	out := map[string]any{"main_title": title, "task_id": taskID}
	if card.DecisionRequired {
		if len(card.Questions) != 0 || !validWecomHumanText(card.Description, 112, true) {
			return nil, false
		}
		out["card_type"] = wecomHumanButton
		out["sub_title_text"] = card.Description
		out["button_list"] = []map[string]any{
			{"text": "同意", "style": 1, "key": "approve"},
			{"text": "拒绝", "style": 2, "key": "reject"},
		}
		return out, true
	}
	if len(card.Questions) == 0 || len(card.Questions) > 3 ||
		card.Description != "" && !validWecomHumanText(card.Description, 30, true) {
		return nil, false
	}
	if card.Description != "" {
		title["desc"] = card.Description
	}
	if len(card.Questions) == 1 {
		return renderWecomHumanVote(out, card.Questions[0], card.Title, card.Description)
	}
	return renderWecomHumanDropdowns(out, card.Questions)
}

func renderWecomHumanVote(out map[string]any, q humanConfirmationQuestion, title, description string) (map[string]any, bool) {
	// The checkbox template has no separate question title. Its wording must be
	// visible in the existing title or description.
	if !validWecomHumanText(q.Text, 30, false) || q.Text != title && !strings.Contains(description, q.Text) {
		return nil, false
	}
	options, ok := wecomHumanQuestionOptions(q, 20, 11)
	if !ok {
		return nil, false
	}
	var mode int
	key := wecomHumanSingleKey
	if q.AnswerType == "multi_choice" {
		mode, key = 1, wecomHumanMultiKey
	}
	out["card_type"] = wecomHumanVote
	out["checkbox"] = map[string]any{"question_key": q.ID, "mode": mode, "option_list": options}
	out["submit_button"] = map[string]string{"text": "提交答复", "key": key}
	return out, true
}

func renderWecomHumanDropdowns(out map[string]any, questions []humanConfirmationQuestion) (map[string]any, bool) {
	selects := make([]map[string]any, 0, len(questions))
	seen := make(map[string]bool, len(questions))
	for _, q := range questions {
		if q.AnswerType != "choice" || seen[q.ID] || !validWecomHumanText(q.Text, 13, false) {
			return nil, false
		}
		// The provider preselects the first dropdown option otherwise. Give it
		// an explicit non-answer, not a real answer the user did not pick.
		choices, ok := wecomHumanQuestionOptions(q, 9, 10)
		if !ok {
			return nil, false
		}
		selects = append(selects, map[string]any{
			"question_key": q.ID,
			"title":        q.Text,
			"selected_id":  wecomHumanChoiceEmpty,
			"option_list":  append([]map[string]string{{"id": wecomHumanChoiceEmpty, "text": "请选择"}}, choices...),
		})
		seen[q.ID] = true
	}
	out["card_type"] = wecomHumanDropdown
	out["select_list"] = selects
	out["submit_button"] = map[string]string{"text": "提交答复", "key": wecomHumanDropdownKey}
	return out, true
}

func wecomHumanQuestionOptions(q humanConfirmationQuestion, limit, labelLimit int) ([]map[string]string, bool) {
	if !validWecomHumanKey(q.ID, 80, true) || q.AnswerType != "choice" && q.AnswerType != "multi_choice" ||
		len(q.Options) == 0 || len(q.Options) > limit {
		return nil, false
	}
	choices := make([]map[string]string, 0, len(q.Options))
	seen := make(map[string]bool, len(q.Options))
	for _, option := range q.Options {
		if !validWecomHumanKey(option.ID, 64, false) || option.ID == wecomHumanChoiceEmpty || seen[option.ID] ||
			!validWecomHumanText(option.Label, labelLimit, false) {
			return nil, false
		}
		choices = append(choices, map[string]string{"id": option.ID, "text": option.Label})
		seen[option.ID] = true
	}
	return choices, true
}

func validWecomHumanText(s string, limit int, multiline bool) bool {
	return strings.TrimSpace(s) != "" && utf8.ValidString(s) && utf8.RuneCountInString(s) <= limit &&
		strings.IndexFunc(s, func(r rune) bool {
			return (r != '\n' || !multiline) && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r))
		}) < 0
}

func validWecomHumanKey(s string, limit int, allowAt bool) bool {
	return len(s) > 0 && len(s) <= limit && strings.IndexFunc(s, func(r rune) bool {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', allowAt && r == '@':
			return false
		default:
			return true
		}
	}) < 0
}

type wecomHumanCardEvent struct {
	MessageID, ChatID, ActorID, TaskID, CardType, Decision string
	Answers                                                []humanCardAnswerItem
}

type wecomHumanCardCallback struct {
	TaskID   string                  `json:"task_id"`
	Key      string                  `json:"event_key"`
	CardType string                  `json:"card_type"`
	Selected *wecomHumanCardSelected `json:"selected_items"`
}

type wecomHumanCardSelected struct {
	Items []struct {
		Question string `json:"question_key"`
		Options  struct {
			IDs []string `json:"option_id"`
		} `json:"option_ids"`
	} `json:"selected_item"`
}

// parseWecomHumanCardEvent only parses a callback attributable to the bound
// bot in a DM. The caller must still match the sent card, intended actor and
// current binding, then submit it to the authoritative Manager.
func parseWecomHumanCardEvent(f wecomFrame, b Binding) (wecomHumanCardEvent, bool) {
	var in struct {
		MessageID string `json:"msgid"`
		BotID     string `json:"aibotid"`
		ChatID    string `json:"chatid"`
		ChatType  string `json:"chattype"`
		Type      string `json:"msgtype"`
		From      struct {
			UserID string `json:"userid"`
		} `json:"from"`
		Event struct {
			Type string `json:"eventtype"`
			wecomHumanCardCallback
			Nested *wecomHumanCardCallback `json:"template_card_event"`
		} `json:"event"`
	}
	if b.Provider != "wecom" || f.Cmd != "aibot_event_callback" || len(f.Body) > 16<<10 || !validWecomID(f.Headers.ReqID) ||
		json.Unmarshal(f.Body, &in) != nil || in.Type != "event" || in.Event.Type != "template_card_event" ||
		b.Credentials.AppID == "" || b.Credentials.AppID != in.BotID || !validWecomID(in.MessageID) ||
		!validWecomID(in.From.UserID) || in.From.UserID == in.BotID || !wecomHumanPrivateCallback(in.ChatType, in.ChatID, in.From.UserID) {
		return wecomHumanCardEvent{}, false
	}
	c := in.Event.wecomHumanCardCallback
	if in.Event.Nested != nil {
		// Some SDK/provider versions nest the action; never mix it with
		// outer selections or authorization fields.
		if c != (wecomHumanCardCallback{}) {
			return wecomHumanCardEvent{}, false
		}
		c = *in.Event.Nested
	}
	cardType, decision, answers, ok := interpretWecomHumanCardAction(c)
	if !validWecomHumanKey(c.TaskID, 128, true) || !ok {
		return wecomHumanCardEvent{}, false
	}
	return wecomHumanCardEvent{
		MessageID: in.MessageID, ChatID: in.From.UserID, ActorID: in.From.UserID, TaskID: c.TaskID,
		CardType: cardType, Decision: decision, Answers: answers,
	}, true
}

func wecomHumanPrivateCallback(chatType, chatID, actor string) bool {
	return chatType == "single" && (chatID == "" || chatID == actor) || chatType == "" && chatID == ""
}

func interpretWecomHumanCardAction(c wecomHumanCardCallback) (string, string, []humanCardAnswerItem, bool) {
	var cardType string
	decision := "information"
	switch c.Key {
	case "approve", "reject":
		cardType, decision = wecomHumanButton, c.Key
	case wecomHumanSingleKey, wecomHumanMultiKey:
		cardType = wecomHumanVote
	case wecomHumanDropdownKey:
		cardType = wecomHumanDropdown
	default:
		return "", "", nil, false
	}
	if c.CardType != "" && c.CardType != cardType {
		return "", "", nil, false
	}
	if cardType == wecomHumanButton {
		return cardType, decision, nil, c.Selected == nil || len(c.Selected.Items) == 0
	}
	answers, ok := decodeWecomHumanChoices(c.Key, c.Selected)
	return cardType, decision, answers, ok
}

func decodeWecomHumanChoices(key string, selected *wecomHumanCardSelected) ([]humanCardAnswerItem, bool) {
	dropdown := key == wecomHumanDropdownKey
	multi := key == wecomHumanMultiKey
	if selected == nil || dropdown && (len(selected.Items) < 2 || len(selected.Items) > 3) || !dropdown && len(selected.Items) != 1 {
		return nil, false
	}
	out := make([]humanCardAnswerItem, 0, len(selected.Items))
	seen := make(map[string]bool, len(selected.Items))
	for _, item := range selected.Items {
		ids := item.Options.IDs
		if !validWecomHumanKey(item.Question, 80, true) || seen[item.Question] ||
			len(ids) == 0 || len(ids) > 20 || !multi && len(ids) != 1 {
			return nil, false
		}
		seen[item.Question] = true
		if dropdown && ids[0] == wecomHumanChoiceEmpty {
			continue
		}
		for i, id := range ids {
			if !validWecomHumanKey(id, 64, false) || id == wecomHumanChoiceEmpty || slices.Contains(ids[:i], id) {
				return nil, false
			}
		}
		answer := humanCardAnswerItem{QuestionID: item.Question}
		if multi {
			answer.OptionIDs = slices.Clone(ids)
			slices.Sort(answer.OptionIDs)
		} else {
			answer.Value = ids[0]
		}
		out = append(out, answer)
	}
	return out, len(out) > 0
}

// renderWecomHumanCardUpdate is for a terminal answer accepted by Manager.
// The provider requires a card_action for text_notice even during an update.
// Use the fixed WeCom homepage (no business-data URL and no action buttons).
func renderWecomHumanCardUpdate(taskID, actorID, decision string) (map[string]any, bool) {
	label := map[string]string{"approve": "已同意", "reject": "已拒绝", "information": "已提交"}[decision]
	if label == "" || !validWecomHumanKey(taskID, 128, true) || !validWecomID(actorID) {
		return nil, false
	}
	return map[string]any{
		"response_type": "update_template_card",
		"userids":       []string{actorID},
		"template_card": map[string]any{
			"card_type":   "text_notice",
			"task_id":     taskID,
			"card_action": map[string]any{"type": 1, "url": wecomHumanUpdateAction},
			"main_title":  map[string]string{"title": label, "desc": "答复已记录，无需再次操作。"},
		},
	}, true
}
