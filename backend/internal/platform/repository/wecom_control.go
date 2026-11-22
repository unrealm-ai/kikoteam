package repository

import (
	"context"
	"database/sql"
	"errors"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"regexp"
)

var wecomCodeNumbers = regexp.MustCompile(`[0-9]+`)

// Used/cancelled codes retire at completion; naturally expired codes retire at expiry.
const wecomCodeRetentionEnd = `LEAST(expires_at,COALESCE(completed_at,expires_at))+interval '72 hours'`

func singleWecomCode(text string) string {
	found := ""
	for _, n := range wecomCodeNumbers.FindAllString(text, -1) {
		if len(n) == 6 {
			if found != "" {
				return ""
			}
			found = n
		}
	}
	return found
}

// ConsumeWecomControl runs after trusted ingress identity validation and under its
// transaction. Recognized control codes never reach Inbox, Input, logs or the model.
func ConsumeWecomControl(secret []byte) func(context.Context, *sql.Tx, manager.ChannelInbound, int64) (manager.WecomControlResult, error) {
	return func(ctx context.Context, tx *sql.Tx, v manager.ChannelInbound, epoch int64) (manager.WecomControlResult, error) {
		var ordinaryResult manager.WecomControlResult
		handled := manager.WecomControlResult{Handled: true}
		if !v.ControlEligible || v.ChatType != "direct" || v.ThreadID != "" || v.MessageType != "text" || v.QuotedText != "" || len(secret) < 32 {
			return ordinaryResult, nil
		}
		code := singleWecomCode(v.Text)
		if code == "" {
			return ordinaryResult, nil
		}
		var w, a string
		if e := tx.QueryRowContext(ctx, `SELECT workspace_id,agent_id FROM platform_channel_bindings WHERE id=$1 AND provider='wecom'`, v.BindingID).Scan(&w, &a); e != nil {
			return ordinaryResult, e
		}
		var consumed, ordinary bool
		if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_wecom_control_events WHERE binding_id=$1 AND identity_epoch=$2 AND (provider_event_id=$3 OR provider_message_id=$4)), EXISTS(SELECT 1 FROM manager_channel_inbox WHERE binding_id=$1 AND identity_epoch=$2 AND (provider_event_id=$3 OR provider_message_id=$4))`, v.BindingID, epoch, v.ProviderEventID, v.ProviderMessageID).Scan(&consumed, &ordinary); e != nil {
			return ordinaryResult, e
		}
		if consumed {
			return handled, nil
		}
		if ordinary {
			return ordinaryResult, nil
		}
		var pending bool
		if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_wecom_verifications WHERE binding_id=$1 AND status='pending' AND expires_at>now())`, v.BindingID).Scan(&pending); e != nil {
			return ordinaryResult, e
		}
		attempts, total := 0, 0
		if pending {
			if e := tx.QueryRowContext(ctx, `SELECT COALESCE(sum(attempts),0) FROM platform_wecom_verification_attempts WHERE binding_id=$1 AND window_start=date_trunc('hour',now())`, v.BindingID).Scan(&total); e != nil {
				return ordinaryResult, e
			}
		}
		if pending && total < 100 {
			if e := tx.QueryRowContext(ctx, `INSERT INTO platform_wecom_verification_attempts(binding_id,sender_id,window_start,attempts) VALUES($1,$2,date_trunc('hour',now()),1) ON CONFLICT(binding_id,sender_id,window_start) DO UPDATE SET attempts=platform_wecom_verification_attempts.attempts+1 RETURNING attempts`, v.BindingID, v.SenderID).Scan(&attempts); e != nil {
				return ordinaryResult, e
			}
		}
		var id, binding, user, state string
		var storedEpoch, gen int64
		var unexpired, completedBeforeExpiry bool
		e := tx.QueryRowContext(ctx, `SELECT id,binding_id,identity_epoch,connection_generation,search_user_id,status,expires_at>now(),COALESCE(completed_at<expires_at,false)
 FROM platform_wecom_verifications WHERE code_hash=$1 AND `+wecomCodeRetentionEnd+`>now() FOR UPDATE`, service.WecomCodeHash(secret, w, a, code)).Scan(&id, &binding, &storedEpoch, &gen, &user, &state, &unexpired, &completedBeforeExpiry)
		if errors.Is(e, sql.ErrNoRows) {
			return ordinaryResult, nil
		}
		if e != nil {
			return ordinaryResult, e
		}
		result, e := tx.ExecContext(ctx, `INSERT INTO platform_wecom_control_events(binding_id,identity_epoch,provider_event_id,provider_message_id) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, v.BindingID, epoch, v.ProviderEventID, v.ProviderMessageID)
		if e != nil {
			return handled, e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return handled, e
		}
		if n == 0 {
			return handled, nil
		}
		if binding != v.BindingID || storedEpoch != epoch || gen != v.ConnectionGeneration || attempts > 10 || total >= 100 {
			return handled, nil
		}
		if state != "pending" || !unexpired {
			handled.Reply = retiredWecomReply(state, completedBeforeExpiry)
			return handled, nil
		}
		result, e = tx.ExecContext(ctx, `INSERT INTO platform_wecom_identity_mappings(binding_id,identity_epoch,connection_generation,search_user_id,callback_user_id,verification_id) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, binding, epoch, gen, user, v.SenderID, id)
		if e != nil {
			return handled, e
		}
		n, e = result.RowsAffected()
		if e != nil {
			return handled, e
		}
		status := "verified"
		callback := v.SenderID
		if n == 0 {
			status = "conflict"
			callback = ""
		} else {
			handled.Reply = manager.WecomControlVerified
		}
		_, e = tx.ExecContext(ctx, `UPDATE platform_wecom_verifications SET status=$2,callback_user_id=$3,completed_at=now(),code_ciphertext='' WHERE id=$1`, id, status, callback)
		return handled, e
	}
}

func retiredWecomReply(state string, completedBeforeExpiry bool) manager.WecomControlReply {
	if !completedBeforeExpiry {
		return manager.WecomControlExpired
	}
	if state == "verified" {
		return manager.WecomControlUsed
	}
	return manager.WecomControlRevoked
}
