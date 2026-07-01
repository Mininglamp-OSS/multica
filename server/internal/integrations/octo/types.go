package octo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// UID is an Octo user's identifier within a deployment. Typed alias rather than
// a plain string so callers can't accidentally pass a Multica user UUID where an
// Octo uid is expected.
type UID string

// ChannelType discriminates conversation kinds, matching the WuKongIM channel
// types carried on the wire.
type ChannelType int16

const (
	ChannelDM    ChannelType = 1 // direct (1:1) message
	ChannelGroup ChannelType = 2 // group chat
	ChannelTopic ChannelType = 5 // community topic / thread
)

// InstallationStatus mirrors the channel_installation.status CHECK constraint.
type InstallationStatus string

const (
	InstallationActive  InstallationStatus = "active"
	InstallationRevoked InstallationStatus = "revoked"
)

// BindingTokenTTL caps the lifetime of a member-binding token. The DB CHECK on
// channel_binding_token enforces the same bound at the storage layer. Keep these
// in sync if the product value changes.
const BindingTokenTTL = 15 * time.Minute

// Outcome categorizes what the inbound pipeline decided to do with a message.
// Values match engine.Outcome 1:1 so the engine verdict maps straight across.
type Outcome string

const (
	OutcomeDropped       Outcome = "dropped"
	OutcomeNeedsBinding  Outcome = "needs_binding"
	OutcomeIngested      Outcome = "ingested"
	OutcomeAgentOffline  Outcome = "agent_offline"
	OutcomeAgentArchived Outcome = "agent_archived"
)

// replyContext is the minimal, channel_*-agnostic context the OutcomeReplier
// needs to react to an engine verdict: where the message came from and what the
// engine decided. It is reconstructed by octoOutboundReplier from the engine
// Result + the inbound message (octo_resolvers.go).
type replyContext struct {
	ChannelID   string
	ChannelType ChannelType
	SenderUID   UID
	Outcome     Outcome
}

// TxStarter abstracts transaction creation. Satisfied by *pgxpool.Pool. Used by
// BindingTokenService.RedeemAndBind to consume the token and write the binding
// atomically.
type TxStarter interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}
