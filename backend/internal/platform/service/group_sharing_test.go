package service

import "testing"

func sharingTestRef(w, a, p, kind, id string) SelectionRef {
	r := SelectionRef{Kind: kind, Provider: p, WorkspaceID: w, AgentID: a, ExternalID: id}
	if kind == "group" {
		r.ID = NewID()
	}
	if p != "web" {
		r.BindingID = NewID()
		r.IdentityEpoch = 1
		r.Generation = 1
		r.TenantID = "tenant"
		r.Namespace = "chat_id"
	}
	return r
}
func TestSharingPolicyDirectionsAndCapabilities(t *testing.T) {
	w, a := NewID(), NewID()
	web := sharingTestRef(w, a, "web", "group", NewID())
	feishu := sharingTestRef(w, a, "feishu", "group", "group-a")
	person := sharingTestRef(w, a, "web", "user", NewID())
	q := GroupSharingPut{Name: "信息同步", Mode: "directed", Sources: []SelectionRef{web}, Targets: []SelectionRef{feishu, person}}
	edges, err := SharingPolicyEdges(w, a, q)
	if err != nil || len(edges) != 2 {
		t.Fatal(edges, err)
	}
	for _, e := range edges {
		if e.Source != web || e.HistoryCapability != "local_history" || e.HistoryPermissionVerified {
			t.Fatal(e)
		}
	}
	q.Mode = "mutual"
	q.Sources = []SelectionRef{web, feishu}
	q.Targets = []SelectionRef{}
	edges, err = SharingPolicyEdges(w, a, q)
	if err != nil || len(edges) != 2 || edges[0].Source != web || edges[1].Source != feishu || edges[1].HistoryCapability != "provider_history" {
		t.Fatal(edges, err)
	}
	q.Sources = append(q.Sources, sharingTestRef(w, a, "dingtalk", "group", "ding"))
	edges, err = SharingPolicyEdges(w, a, q)
	if err != nil || len(edges) != 6 {
		t.Fatal(edges, err)
	}
	if edges[4].HistoryCapability != "unsupported" || edges[4].UnavailableReason != "history_not_supported" {
		t.Fatal(edges[4])
	}
}
func TestSharingPolicyRejectsImplicitAudienceAndMalformedRefs(t *testing.T) {
	w, a := NewID(), NewID()
	source := sharingTestRef(w, a, "web", "group", NewID())
	target := sharingTestRef(w, a, "wecom", "group", "chat")
	base := GroupSharingPut{Name: "Policy", Mode: "directed", Sources: []SelectionRef{source}, Targets: []SelectionRef{target}}
	cases := []GroupSharingPut{base, base, base, base, base, base}
	cases[0].Targets = []SelectionRef{source}
	cases[1].Mode = "mutual"
	cases[2].Targets = []SelectionRef{}
	cases[3].Sources = []SelectionRef{source, source}
	department := target
	department.Kind = "department"
	cases[4].Targets = []SelectionRef{department}
	foreign := target
	foreign.WorkspaceID = NewID()
	cases[5].Targets = []SelectionRef{foreign}
	for i, q := range cases {
		if _, err := SharingPolicyEdges(w, a, q); err == nil {
			t.Fatal("accepted invalid policy", i)
		}
	}
}
