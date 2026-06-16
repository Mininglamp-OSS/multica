package main

import (
	"log/slog"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/integrations/outwebhook"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// registerWebhookListeners wires the outbound webhook dispatcher to the event
// bus. v1 listens only for issue status changes and POSTs a signed payload to
// every matching webhook_subscription (workspace-level or project-level).
//
// This mirrors registerAutopilotListeners: it filters issue:updated events on
// status_changed and reads the typed handler.IssueResponse out of the payload.
// Delivery itself is async (the dispatcher detaches each POST), so this listener
// never blocks the synchronous bus dispatch.
//
// Scope of issue.status_changed (v1): it fires for the user/API/PR-merge status
// transitions that publish issue:updated with status_changed=true — single
// update, batch update, and the GitHub PR-merged path. It does NOT fire for
// system-internal status mutations that bypass that event, e.g. the agent
// task-failure reset and the stuck-issue sweeper (in_progress → todo), which
// write status directly via UpdateIssueStatus and publish only task events.
// This matches how the existing inbound autopilot webhooks consume the same
// event; widening the producer to emit status_changed on every internal path is
// out of scope for this feature. Subscribers should treat the event as
// "user/API-initiated status change", not "every possible status mutation".
func registerWebhookListeners(bus *events.Bus, d *outwebhook.Dispatcher) {
	bus.Subscribe(protocol.EventIssueUpdated, func(e events.Event) {
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			return
		}
		statusChanged, _ := payload["status_changed"].(bool)
		if !statusChanged {
			return
		}
		issue, ok := payload["issue"].(handler.IssueResponse)
		if !ok {
			slog.Debug("webhook listener: issue payload not IssueResponse")
			return
		}
		prevStatus, _ := payload["prev_status"].(string)

		projectID := ""
		if issue.ProjectID != nil {
			projectID = *issue.ProjectID
		}

		d.DispatchIssueStatusChanged(outwebhook.IssueStatusChanged{
			WorkspaceID:    e.WorkspaceID,
			ProjectID:      projectID,
			ActorType:      e.ActorType,
			ActorID:        e.ActorID,
			PreviousStatus: prevStatus,
			Issue:          issue,
		})
	})
}
