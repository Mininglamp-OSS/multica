// Package octo implements the Octo (WuKongIM) IM integration as a
// channel.Channel — the third adapter driven by the shared channel-agnostic
// engine, after Feishu (internal/integrations/lark) and Slack
// (internal/integrations/slack).
//
// HISTORY / WHY THIS SHAPE: Octo originally shipped (migration 120) with its own
// parallel Hub/Dispatcher/Connector stack and octo_* tables, mirroring the
// Feishu-only lark.Hub that existed at the time. After MUL-3515/3506/MUL-3620
// generalized that into the channel.Channel contract + engine.Supervisor/Router
// over the channel_* tables (migration 124), Octo was converged onto it
// (migration 128): the octo_* tables were folded into channel_* with
// channel_type='octo', and the bespoke hub/dispatcher/chat-session machinery was
// deleted in favor of the shared engine. Octo now gets /issue commands, run
// debounce batching, and future engine improvements for free.
//
// The layering is one-directional:
//
//	transport  — WuKongIM binary protocol (socket) + Octo REST client. No
//	             knowledge of business types. Reused verbatim across the
//	             convergence.
//	adapter    — this package. Implements channel.Channel (octo_channel.go) and
//	             the engine.ResolverSet (octo_resolvers.go), plus the HTTP-facing
//	             InstallationService / BindingTokenService and the outbound
//	             Patcher / OutcomeReplier. Depends on transport, the shared engine,
//	             and the generated channel_* queries.
//
// The moving parts:
//
//   - octoChannel (octo_channel.go) implements channel.Channel. Connect opens the
//     WuKongIM long-connection (via the transport subpackage), strips the bot's
//     own @mention, parses a leading /new directive, and hands each message to the
//     engine handler as a normalized channel.InboundMessage. The
//     engine.Supervisor owns the per-installation connection + WS lease.
//   - NewOctoResolverSet (octo_resolvers.go) is the engine.ResolverSet: the
//     installation/identity/dedup/session/audit/replier seams, all built on the
//     generic channel_* queries + the shared engine.ChatSession.
//   - Patcher (outbound.go) relays the agent's reply back to Octo on
//     chat:done / task:failed events (a bus subscriber, not part of the Channel
//     interface).
//   - OutcomeReplier (outcome_replier.go) handles the synchronous, pre-agent
//     outcomes: DM an unbound sender a binding link, or notify the user when the
//     agent is offline/archived.
//   - BindingTokenService (binding_token.go) mints and redeems the one-shot
//     tokens behind the {PublicURL}/octo/bind?token= flow.
//   - InstallationService (client.go) creates/revokes installations and encrypts
//     the bot token at rest inside the channel_installation.config blob.
//
// Expired binding tokens and stale inbound-dedup rows are purged by the
// channel cleanup scheduler job (internal/scheduler/jobs_octo_cleanup.go), which
// now covers every IM platform sharing the channel_* tables. The DB-layer
// queries this package depends on are the generalized channel queries in
// pkg/db/queries/channel.sql (migrations 124 + 128).
package octo
