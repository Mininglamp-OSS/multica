package main

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/handler"
)

func strptr(s string) *string { return &s }

func TestWebhookIssuePayload(t *testing.T) {
	t.Run("typed IssueResponse with project", func(t *testing.T) {
		issue := handler.IssueResponse{ID: "i1", ProjectID: strptr("proj-1")}
		projectID, got, ok := webhookIssuePayload(issue)
		if !ok {
			t.Fatal("expected ok for IssueResponse")
		}
		if projectID != "proj-1" {
			t.Errorf("projectID = %q, want proj-1", projectID)
		}
		if _, isResp := got.(handler.IssueResponse); !isResp {
			t.Errorf("issue body should pass through as IssueResponse")
		}
	})

	t.Run("typed IssueResponse without project", func(t *testing.T) {
		projectID, _, ok := webhookIssuePayload(handler.IssueResponse{ID: "i1"})
		if !ok || projectID != "" {
			t.Errorf("ok=%v projectID=%q, want ok + empty", ok, projectID)
		}
	})

	t.Run("map shape with *string project_id (issueToMap)", func(t *testing.T) {
		m := map[string]any{"id": "i1", "project_id": strptr("proj-2"), "status": "todo"}
		projectID, got, ok := webhookIssuePayload(m)
		if !ok {
			t.Fatal("expected ok for map")
		}
		if projectID != "proj-2" {
			t.Errorf("projectID = %q, want proj-2", projectID)
		}
		if _, isMap := got.(map[string]any); !isMap {
			t.Errorf("issue body should pass through as map")
		}
	})

	t.Run("map shape with nil *string project_id (workspace-level)", func(t *testing.T) {
		var nilp *string
		m := map[string]any{"id": "i1", "project_id": nilp}
		projectID, _, ok := webhookIssuePayload(m)
		if !ok || projectID != "" {
			t.Errorf("ok=%v projectID=%q, want ok + empty", ok, projectID)
		}
	})

	t.Run("map shape with plain string project_id", func(t *testing.T) {
		m := map[string]any{"id": "i1", "project_id": "proj-3"}
		projectID, _, ok := webhookIssuePayload(m)
		if !ok || projectID != "proj-3" {
			t.Errorf("ok=%v projectID=%q, want proj-3", ok, projectID)
		}
	})

	t.Run("unknown shape", func(t *testing.T) {
		if _, _, ok := webhookIssuePayload(42); ok {
			t.Error("expected ok=false for unknown shape")
		}
		if _, _, ok := webhookIssuePayload(nil); ok {
			t.Error("expected ok=false for nil")
		}
	})
}
