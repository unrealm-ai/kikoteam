package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type sharingRefEdge struct{ Source, Target service.SelectionRef }

func validateSharingChanges(ctx context.Context, tx *sql.Tx, w, a string, old service.GroupSharingPolicy, next service.GroupSharingPut, edges []service.GroupSharingEdge) error {
	retained := map[sharingRefEdge]bool{}
	if old.ID != "" && (!next.Enabled || old.Enabled) {
		prior, err := service.SharingPolicyEdges(w, a, service.GroupSharingPut{Name: old.Name, Mode: old.Mode, Enabled: old.Enabled, Sources: old.Sources, Targets: old.Targets})
		if err != nil {
			return err
		}
		for _, edge := range prior {
			retained[sharingRefEdge{edge.Source, edge.Target}] = true
		}
	}
	checked := map[service.SelectionRef]bool{}
	for _, edge := range edges {
		if retained[sharingRefEdge{edge.Source, edge.Target}] {
			continue
		}
		for _, ref := range []service.SelectionRef{edge.Source, edge.Target} {
			if checked[ref] {
				continue
			}
			if err := validateSharingRef(ctx, tx, w, a, ref); err != nil {
				return err
			}
			checked[ref] = true
		}
	}
	return nil
}
func validateSharingRef(ctx context.Context, tx *sql.Tx, w, a string, r service.SelectionRef) error {
	unavailable := service.Fail("sharing_subject_unavailable", 409, "共享主体已失效，请重新选择")
	if r.Kind == "group" {
		if r.ID == "" {
			return unavailable
		}
		group, err := scanGroupSelection(tx.QueryRowContext(ctx, `SELECT * FROM (`+groupSelectionSourcesSQL+`) candidates WHERE provider=$3 AND id=$4`, w, a, r.Provider, r.ID), w, a)
		if errors.Is(err, sql.ErrNoRows) {
			return unavailable
		}
		if err != nil {
			return err
		}
		if group.Ref != r {
			return unavailable
		}
		return nil
	}
	if r.Kind != "user" || r.ID != "" || r.GroupEpoch != 0 || r.AgentEpoch != 0 || r.ConfigRevision != 0 {
		return unavailable
	}
	candidate := service.PeopleCandidate{Ref: r, Status: "unavailable"}
	var err error
	if r.Provider == "web" {
		if r.Namespace != "" {
			return unavailable
		}
		err = tx.QueryRowContext(ctx, `SELECT m.status FROM platform_members m WHERE m.workspace_id=$1 AND m.user_id=$2`, w, r.ExternalID).Scan(&candidate.Status)
	} else {
		err = (&PeopleSelectorStore{}).resolveChannelPerson(ctx, tx, w, a, r, &candidate)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return unavailable
	}
	if err != nil {
		return err
	}
	if candidate.Status != "active" && (r.Provider == "web" || candidate.Status != "unknown") {
		return unavailable
	}
	return nil
}
