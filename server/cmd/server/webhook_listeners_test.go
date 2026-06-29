package main

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/handler"
)

func strptr(s string) *string { return &s }

func TestWebhookIssuePayload(t *testing.T) {
	t.Run("typed IssueResponse with project + assignee", func(t *testing.T) {
		issue := handler.IssueResponse{
			ID:           "i1",
			Identifier:   "MUL-1",
			ProjectID:    strptr("proj-1"),
			AssigneeType: strptr("member"),
			AssigneeID:   strptr("mem-1"),
		}
		f, ok := webhookIssuePayload(issue)
		if !ok {
			t.Fatal("expected ok for IssueResponse")
		}
		if f.projectID != "proj-1" {
			t.Errorf("projectID = %q, want proj-1", f.projectID)
		}
		if f.identifier != "MUL-1" {
			t.Errorf("identifier = %q, want MUL-1", f.identifier)
		}
		if f.assigneeType != "member" || f.assigneeID != "mem-1" {
			t.Errorf("assignee = %q/%q, want member/mem-1", f.assigneeType, f.assigneeID)
		}
		if _, isResp := f.issue.(handler.IssueResponse); !isResp {
			t.Errorf("issue body should pass through as IssueResponse")
		}
	})

	t.Run("typed IssueResponse without project or assignee", func(t *testing.T) {
		f, ok := webhookIssuePayload(handler.IssueResponse{ID: "i1", Identifier: "MUL-2"})
		if !ok || f.projectID != "" || f.assigneeType != "" || f.assigneeID != "" {
			t.Errorf("got %+v ok=%v, want ok + empty project/assignee", f, ok)
		}
		if f.identifier != "MUL-2" {
			t.Errorf("identifier = %q, want MUL-2", f.identifier)
		}
	})

	t.Run("map shape with *string fields (issueToMap)", func(t *testing.T) {
		m := map[string]any{
			"id":            "i1",
			"identifier":    "MUL-3",
			"project_id":    strptr("proj-2"),
			"assignee_type": strptr("agent"),
			"assignee_id":   strptr("agent-9"),
			"status":        "todo",
		}
		f, ok := webhookIssuePayload(m)
		if !ok {
			t.Fatal("expected ok for map")
		}
		if f.projectID != "proj-2" {
			t.Errorf("projectID = %q, want proj-2", f.projectID)
		}
		if f.identifier != "MUL-3" {
			t.Errorf("identifier = %q, want MUL-3", f.identifier)
		}
		if f.assigneeType != "agent" || f.assigneeID != "agent-9" {
			t.Errorf("assignee = %q/%q, want agent/agent-9", f.assigneeType, f.assigneeID)
		}
		if _, isMap := f.issue.(map[string]any); !isMap {
			t.Errorf("issue body should pass through as map")
		}
	})

	t.Run("map shape with nil *string project_id (workspace-level)", func(t *testing.T) {
		var nilp *string
		m := map[string]any{"id": "i1", "project_id": nilp}
		f, ok := webhookIssuePayload(m)
		if !ok || f.projectID != "" {
			t.Errorf("ok=%v projectID=%q, want ok + empty", ok, f.projectID)
		}
	})

	t.Run("map shape with plain string fields", func(t *testing.T) {
		m := map[string]any{"id": "i1", "project_id": "proj-3", "assignee_type": "squad", "assignee_id": "sq-1"}
		f, ok := webhookIssuePayload(m)
		if !ok || f.projectID != "proj-3" {
			t.Errorf("ok=%v projectID=%q, want proj-3", ok, f.projectID)
		}
		if f.assigneeType != "squad" || f.assigneeID != "sq-1" {
			t.Errorf("assignee = %q/%q, want squad/sq-1", f.assigneeType, f.assigneeID)
		}
	})

	t.Run("unknown shape", func(t *testing.T) {
		if _, ok := webhookIssuePayload(42); ok {
			t.Error("expected ok=false for unknown shape")
		}
		if _, ok := webhookIssuePayload(nil); ok {
			t.Error("expected ok=false for nil")
		}
	})
}
