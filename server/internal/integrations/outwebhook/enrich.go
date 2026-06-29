package outwebhook

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
)

// buildIssueURL constructs the absolute web URL of an issue from the configured
// public base URL + the workspace slug + the issue identifier, matching the
// frontend route {publicURL}/{slug}/issues/{identifier} (the issue route
// resolves identifiers as well as UUIDs, so the identifier is link-stable).
//
// Best-effort: returns "" when the public URL is unset, the identifier is empty,
// or the workspace slug can't be resolved — the caller omits issue_url and
// receivers degrade to no link. Never blocks delivery.
func (d *Dispatcher) buildIssueURL(ctx context.Context, workspaceID pgtype.UUID, identifier string) string {
	identifier = strings.TrimSpace(identifier)
	if d.publicURL == "" || identifier == "" {
		return ""
	}
	ws, err := d.store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		slog.Debug("outwebhook: resolve workspace slug for issue_url failed",
			"workspace_id", util.UUIDToString(workspaceID), "error", err)
		return ""
	}
	if ws.Slug == "" {
		return ""
	}
	// publicURL is already trailing-slash trimmed in newWithClient; PathEscape
	// each segment so a slug/identifier with reserved chars can't break the URL.
	return d.publicURL + "/" + url.PathEscape(ws.Slug) + "/issues/" + url.PathEscape(identifier)
}

// resolveAssigneeName maps an issue's polymorphic assignee (assignee_type +
// assignee_id) to a human-readable display name: a member's user name, an
// agent's name, or a squad's name. Returns "" when the issue is unassigned, the
// type is unknown, or the lookup fails — all best-effort, never blocking
// delivery.
func (d *Dispatcher) resolveAssigneeName(ctx context.Context, assigneeType, assigneeID string) string {
	assigneeType = strings.TrimSpace(assigneeType)
	assigneeID = strings.TrimSpace(assigneeID)
	if assigneeType == "" || assigneeID == "" {
		return ""
	}
	id, err := util.ParseUUID(assigneeID)
	if err != nil {
		return ""
	}

	switch assigneeType {
	case "member":
		member, err := d.store.GetMember(ctx, id)
		if err != nil {
			return logAssigneeMiss(assigneeType, assigneeID, err)
		}
		user, err := d.store.GetUser(ctx, member.UserID)
		if err != nil {
			return logAssigneeMiss(assigneeType, assigneeID, err)
		}
		return strings.TrimSpace(user.Name)
	case "agent":
		agent, err := d.store.GetAgent(ctx, id)
		if err != nil {
			return logAssigneeMiss(assigneeType, assigneeID, err)
		}
		return strings.TrimSpace(agent.Name)
	case "squad":
		squad, err := d.store.GetSquad(ctx, id)
		if err != nil {
			return logAssigneeMiss(assigneeType, assigneeID, err)
		}
		return strings.TrimSpace(squad.Name)
	default:
		return ""
	}
}

// logAssigneeMiss logs a best-effort assignee-name lookup miss at debug (a
// missing row is benign — the assignee may have been deleted) and returns "".
func logAssigneeMiss(assigneeType, assigneeID string, err error) string {
	if !errors.Is(err, pgx.ErrNoRows) {
		slog.Debug("outwebhook: resolve assignee name failed",
			"assignee_type", assigneeType, "assignee_id", assigneeID, "error", err)
	}
	return ""
}
