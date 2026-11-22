package channel

import (
	"context"
	"errors"
	"testing"
)

func TestWecomGroupsCompleteFixedWindow(t *testing.T) {
	calls := 0
	want := &WecomGroupRange{BeginMS: 1000, EndMS: 2000, Cursor: "next"}
	list := func(ctx context.Context, _ Binding, r *WecomGroupRange, gate func(context.Context) error) (WecomRecentGroups, error) {
		calls++
		if e := gate(ctx); e != nil {
			return WecomRecentGroups{}, e
		}
		if calls == 1 {
			if r != nil {
				t.Fatal(r)
			}
			return WecomRecentGroups{Items: []WecomRecentGroup{{ID: "a", Name: "A"}}, Next: want}, nil
		}
		if *r != *want {
			t.Fatal(r)
		}
		return WecomRecentGroups{Items: []WecomRecentGroup{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}}, nil
	}
	out, e := collectWecomGroups(context.Background(), Binding{}, list, func(context.Context) error { return nil })
	if e != nil || calls != 2 || len(out) != 2 {
		t.Fatal(out, calls, e)
	}
}
func TestWecomGroupsNoPartialOnFailure(t *testing.T) {
	for _, mode := range []string{"network", "repeat", "range", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			list := func(ctx context.Context, _ Binding, r *WecomGroupRange, gate func(context.Context) error) (WecomRecentGroups, error) {
				calls++
				if e := gate(ctx); e != nil {
					return WecomRecentGroups{}, e
				}
				if calls == 1 {
					return WecomRecentGroups{Items: []WecomRecentGroup{{ID: "a"}}, Next: &WecomGroupRange{BeginMS: 1000, EndMS: 2000, Cursor: "p1"}}, nil
				}
				if mode == "network" {
					return WecomRecentGroups{}, errors.New("failed")
				}
				next := &WecomGroupRange{BeginMS: 1000, EndMS: 2000, Cursor: "p1"}
				if mode == "range" {
					next.BeginMS = 0
					next.Cursor = "p2"
				}
				return WecomRecentGroups{Next: next}, nil
			}
			out, e := collectWecomGroups(context.Background(), Binding{}, list, func(context.Context) error {
				if mode == "revoked" && calls > 1 {
					return errors.New("revoked")
				}
				return nil
			})
			if e == nil || out != nil {
				t.Fatal("partial leaked", out, e)
			}
		})
	}
}
