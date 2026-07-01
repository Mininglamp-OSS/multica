package octo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/integrations/channel"
	"github.com/multica-ai/multica/server/internal/integrations/channel/engine"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// This file is the Octo ResolverSet: the platform-specific seams the
// channel-agnostic engine.Router runs the inbound pipeline through. It mirrors
// the Slack ResolverSet, built entirely on the generic channel_* queries (no new
// query, no schema change) plus the shared engine.ChatSession — so Octo is now
// "implement Channel + register a ResolverSet" like every other platform.

// originOctoChat is the issue.origin_type label written for issues created via
// the Octo /issue command (unchanged from the pre-cutover dispatcher).
const originOctoChat = "octo_chat"

// NewOctoResolverSet assembles the Octo ResolverSet over the generated queries +
// a tx starter (for the shared session service). The optional Replier handles
// the synchronous, pre-agent outcomes (binding prompt, agent offline/archived
// notice); pass nil to disable it.
func NewOctoResolverSet(q *db.Queries, tx engine.TxStarter, replier OutcomeReplier) engine.ResolverSet {
	set := engine.ResolverSet{
		Installation: &installationResolver{q: q},
		Identity:     &identityResolver{q: q},
		Dedup:        &deduper{q: q},
		Session: &sessionBinder{session: engine.NewChatSession(q, tx, TypeOcto, engine.SessionTitles{
			Group:    "Multica feedback",
			Direct:   "Multica conversation",
			Fallback: "Multica chat",
		})},
		Audit:      &auditor{q: q},
		OriginType: originOctoChat,
	}
	if replier != nil {
		set.Replier = &octoOutboundReplier{replier: replier}
	}
	return set
}

var (
	_ engine.InstallationResolver = (*installationResolver)(nil)
	_ engine.IdentityResolver     = (*identityResolver)(nil)
	_ engine.Deduper              = (*deduper)(nil)
	_ engine.SessionBinder        = (*sessionBinder)(nil)
	_ engine.Auditor              = (*auditor)(nil)
)

// octoBindingConfig is the opaque outbound routing persisted on the chat
// binding's config: the numeric WuKongIM channel type, which the outbound path
// needs to send back with the right channel kind (the cross-platform
// channel.OutboundMessage does not carry it).
type octoBindingConfig struct {
	ChannelType int16 `json:"channel_type"`
}

func decodeOctoRaw(msg channel.InboundMessage) (octoRawEvent, error) {
	var raw octoRawEvent
	if len(msg.Raw) == 0 {
		return octoRawEvent{}, errors.New("octo: inbound message Raw is empty")
	}
	if err := json.Unmarshal(msg.Raw, &raw); err != nil {
		return octoRawEvent{}, fmt.Errorf("decode octo inbound raw: %w", err)
	}
	return raw, nil
}

func nullText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// ---- installation routing ----

type installationResolver struct{ q *db.Queries }

func (r *installationResolver) ResolveInstallation(ctx context.Context, msg channel.InboundMessage) (engine.ResolvedInstallation, error) {
	raw, err := decodeOctoRaw(msg)
	if err != nil {
		return engine.ResolvedInstallation{}, err
	}
	inst, err := r.q.GetChannelInstallationByAppID(ctx, db.GetChannelInstallationByAppIDParams{
		ChannelType: string(TypeOcto),
		AppID:       raw.RobotID, // Octo robot_id is stored in the routing-key slot
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return engine.ResolvedInstallation{}, engine.ErrInstallationNotFound
		}
		return engine.ResolvedInstallation{}, err
	}
	return engine.ResolvedInstallation{
		ID:              inst.ID,
		WorkspaceID:     inst.WorkspaceID,
		AgentID:         inst.AgentID,
		InstallerUserID: inst.InstallerUserID,
		Active:          inst.Status == "active",
		Platform:        inst,
	}, nil
}

// ---- identity ----

type identityResolver struct{ q *db.Queries }

func (r *identityResolver) ResolveSender(ctx context.Context, inst engine.ResolvedInstallation, msg channel.InboundMessage) (engine.ResolvedIdentity, error) {
	binding, err := r.q.GetChannelUserBindingByUserID(ctx, db.GetChannelUserBindingByUserIDParams{
		InstallationID: inst.ID,
		ChannelUserID:  msg.Source.SenderID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return engine.ResolvedIdentity{}, engine.ErrSenderUnbound
		}
		return engine.ResolvedIdentity{}, err
	}
	// Binding existence no longer proves membership (no FK); re-check.
	if _, err := r.q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      binding.MulticaUserID,
		WorkspaceID: inst.WorkspaceID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return engine.ResolvedIdentity{}, engine.ErrSenderNotMember
		}
		return engine.ResolvedIdentity{}, err
	}
	return engine.ResolvedIdentity{UserID: binding.MulticaUserID}, nil
}

// ---- dedup ----

type deduper struct{ q *db.Queries }

func (r *deduper) Claim(ctx context.Context, installationID pgtype.UUID, messageID string) (pgtype.UUID, error) {
	claim, err := r.q.ClaimChannelInboundDedup(ctx, db.ClaimChannelInboundDedupParams{
		InstallationID: installationID,
		MessageID:      messageID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pgtype.UUID{}, engine.ErrDuplicate
		}
		return pgtype.UUID{}, err
	}
	return claim.ClaimToken, nil
}

func (r *deduper) Mark(ctx context.Context, installationID pgtype.UUID, messageID string, claimToken pgtype.UUID) error {
	_, err := r.q.MarkChannelInboundDedupProcessed(ctx, db.MarkChannelInboundDedupProcessedParams{
		InstallationID: installationID,
		MessageID:      messageID,
		ClaimToken:     claimToken,
	})
	return err
}

func (r *deduper) Release(ctx context.Context, installationID pgtype.UUID, messageID string, claimToken pgtype.UUID) error {
	_, err := r.q.ReleaseChannelInboundDedup(ctx, db.ReleaseChannelInboundDedupParams{
		InstallationID: installationID,
		MessageID:      messageID,
		ClaimToken:     claimToken,
	})
	return err
}

// ---- session bind / append ----

type chatSession interface {
	EnsureSession(ctx context.Context, in engine.EnsureSessionInput) (pgtype.UUID, error)
	AppendUserMessage(ctx context.Context, in engine.AppendInput) (engine.AppendResult, error)
}

type sessionBinder struct{ session chatSession }

func (r *sessionBinder) EnsureSession(ctx context.Context, p engine.EnsureSessionParams) (pgtype.UUID, error) {
	raw, err := decodeOctoRaw(p.Message)
	if err != nil {
		return pgtype.UUID{}, err
	}
	// Persist the numeric WuKongIM channel type on the binding config so the
	// outbound path can reply with the exact channel kind. Octo has no thread
	// concept, so the chat id is the session-isolation key directly.
	cfg, _ := json.Marshal(octoBindingConfig{ChannelType: raw.ChannelType})
	return r.session.EnsureSession(ctx, engine.EnsureSessionInput{
		WorkspaceID:    p.Installation.WorkspaceID,
		AgentID:        p.Installation.AgentID,
		InstallationID: p.Installation.ID,
		Sender:         p.Sender,
		BindingKey:     p.Message.Source.ChatID,
		BindingConfig:  cfg,
		ChatType:       p.Message.Source.ChatType,
	})
}

func (r *sessionBinder) AppendMessage(ctx context.Context, p engine.AppendParams) (engine.AppendResult, error) {
	return r.session.AppendUserMessage(ctx, engine.AppendInput{
		SessionID:      p.SessionID,
		Sender:         p.Sender,
		InstallationID: p.InstallationID,
		Body:           p.Message.Text,
		// Octo text is not enriched, so the command source is the body itself.
		CommandText: p.Message.Text,
		MessageID:   p.Message.MessageID,
		ClaimToken:  p.ClaimToken,
	})
}

// ---- audit ----

type auditor struct{ q *db.Queries }

func (r *auditor) RecordDrop(ctx context.Context, instID pgtype.UUID, msg channel.InboundMessage, reason engine.DropReason) error {
	return r.q.RecordChannelInboundDrop(ctx, db.RecordChannelInboundDropParams{
		ChannelType:      string(TypeOcto),
		EventType:        "", // Octo events carry no separate event_type
		DropReason:       string(reason),
		InstallationID:   instID,
		ChannelChatID:    nullText(msg.Source.ChatID),
		ChannelEventID:   nullText(msg.EventID),
		ChannelMessageID: nullText(msg.MessageID),
	})
}

// ---- outbound replier ----

// octoOutboundReplier adapts the engine's outbound verdict to the Octo
// OutcomeReplier (binding prompt, agent offline/archived notice). It pulls the
// resolved channel_installation off the engine Result's installation Platform
// and reconstructs the minimal InboundMessage the replier needs.
type octoOutboundReplier struct{ replier OutcomeReplier }

func (r *octoOutboundReplier) Reply(ctx context.Context, inst engine.ResolvedInstallation, msg channel.InboundMessage, res engine.Result) {
	row, ok := inst.Platform.(db.ChannelInstallation)
	if !ok {
		return
	}
	raw, _ := decodeOctoRaw(msg)
	r.replier.Reply(ctx, row, replyContext{
		ChannelID:   msg.Source.ChatID,
		ChannelType: ChannelType(raw.ChannelType),
		SenderUID:   UID(res.Sender),
		Outcome:     Outcome(string(res.Outcome)),
	})
}
