package repository

import (
	"context"
	"database/sql"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

const dingChannelAttempt = `SELECT binding_id,connection_generation,authorized_at,provider_request_id
FROM manager_channel_deliveries WHERE id=$1`

const dingHumanAttempt = `SELECT binding_id,connection_generation,authorized_at,provider_request_id
FROM manager_human_notices WHERE id=$1`

const dingTriggerAttempt = `SELECT (target->>'binding_id')::uuid,(target->>'generation')::bigint,authorized_at,provider_request_id
FROM manager_trigger_notifications WHERE id=$1`

func recordDingProactiveSend(ctx context.Context, tx *sql.Tx, provider, attemptSQL, id string, v service.DeliveryCompletion) error {
	if provider != "dingtalk" {
		return nil
	}
	missing, observed := dingProactiveOutcome(v)
	if !observed {
		return nil
	}
	// Compare the original send authorization, not receipt arrival. A success
	// row is a watermark and wins exact timestamp ties; the token provides a
	// deterministic tie-break between otherwise equivalent observations.
	_, err := tx.ExecContext(ctx, `INSERT INTO manager_dingtalk_proactive_send_observations AS observation
 (binding_id,connection_generation,authorized_at,attempt_token,permission_missing)
 SELECT a.binding_id,a.generation,a.authorized_at,$3,$2
 FROM (`+attemptSQL+`) AS a(binding_id,generation,authorized_at,provider_request_id)
 JOIN platform_channel_bindings b ON b.id=a.binding_id AND b.provider='dingtalk'
   AND b.enabled AND b.connection_generation=a.generation
 WHERE a.authorized_at IS NOT NULL AND (NOT $2::boolean OR a.provider_request_id IS NULL)
 ON CONFLICT (binding_id) DO UPDATE SET connection_generation=EXCLUDED.connection_generation,
 authorized_at=EXCLUDED.authorized_at,attempt_token=EXCLUDED.attempt_token,permission_missing=EXCLUDED.permission_missing
 WHERE (EXCLUDED.connection_generation,EXCLUDED.authorized_at,NOT EXCLUDED.permission_missing,EXCLUDED.attempt_token)
   > (observation.connection_generation,observation.authorized_at,NOT observation.permission_missing,observation.attempt_token)`, id, missing, v.LeaseToken)
	return err
}

func dingProactiveOutcome(v service.DeliveryCompletion) (missing, observed bool) {
	if v.Status == "sent" && v.EvidenceType == "status_query" {
		return false, true
	}
	// The proven permission denial does not change the conservative transport
	// status (unknown) or retry policy. No other failure warns.
	if (v.Status == "unknown" || v.Status == "failed") && v.EvidenceType == "send_response" &&
		v.QueryToken == "" && v.ErrorCode == "dingtalk_proactive_permission_denied" {
		return true, true
	}
	return false, false
}
