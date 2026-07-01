// Package octo is the Octo (WuKongIM) implementation of channel.Channel — the
// third adapter driven by the channel-agnostic engine (after Feishu and Slack),
// converging the integration that originally shipped its own parallel
// Hub/Dispatcher/Connector stack (migration 120, pre-MUL-3620) onto the shared
// engine.Supervisor + engine.Router.
//
// Connect runs the WuKongIM long-connection receive loop (reusing the transport
// subpackage verbatim) and hands every decoded message to the engine's shared
// inbound handler as a normalized channel.InboundMessage; Send posts a reply via
// the Octo REST API. The DB-backed installation/identity/dedup/session seams are
// the engine.ResolverSet in octo_resolvers.go, built on the generalized
// channel_* tables (channel_type='octo').
package octo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/multica-ai/multica/server/internal/integrations/channel"
	"github.com/multica-ai/multica/server/internal/integrations/octo/transport"
)

// TypeOcto is the channel discriminator for the Octo adapter. Defined here (not
// in the channel core package) so registering a new platform never edits the
// core, matching slack.TypeSlack.
const TypeOcto channel.Type = "octo"

// octoChannel is the Octo implementation of channel.Channel. One instance is
// built per channel_installation by the registered Factory. It holds only what
// Connect/Send need (decoded credentials + a logger); the installation identity
// is resolved per message by the Router, so it is absent here — the same split
// the Feishu and Slack adapters use.
type octoChannel struct {
	creds   credentials
	handler channel.InboundHandler
	logger  *slog.Logger

	// newSocket builds the transport.Socket for a connection. A field so tests
	// can inject a fake without a live WebSocket; production uses transport.NewSocket.
	newSocket func(transport.SocketOptions) socketConn
	// register obtains the im_token + ws_url for a connection. A field for the
	// same test-seam reason; production calls the Octo REST register endpoint.
	register func(ctx context.Context) (*transport.BotRegisterResp, error)
}

// socketConn is the slice of transport.Socket the adapter drives, declared as an
// interface so Connect can be unit-tested with a fake socket.
type socketConn interface {
	Connect(ctx context.Context) error
	Disconnect()
}

var _ channel.Channel = (*octoChannel)(nil)

func (c *octoChannel) Type() channel.Type { return TypeOcto }

// Connect registers the bot to obtain an im_token + ws_url, opens the WuKongIM
// WebSocket, and blocks until ctx is cancelled or the connection terminally
// fails — the contract engine.Supervisor relies on to tie lease renewal to
// connection liveness (matching feishuChannel.Connect / slackChannel.Connect).
// Each decoded message is normalized to a channel.InboundMessage and handed to
// the engine handler. Reconnect/backoff within a held connection is the
// transport.Socket's own concern; a terminal socket error unwinds Connect so the
// Supervisor reconnects under backoff.
func (c *octoChannel) Connect(ctx context.Context) error {
	if c.handler == nil {
		return errors.New("octo: inbound handler not configured")
	}

	// Register to obtain im_token + ws_url (the bot token alone can't open the WS).
	reg, err := c.register(ctx)
	if err != nil {
		return fmt.Errorf("octo: register bot: %w", err)
	}

	// A terminal socket error (kicked, rapid disconnect, stale im_token) stops
	// the socket's internal reconnect loop and fires OnError. Surface it through
	// this channel so Connect unwinds and the Supervisor rebuilds the channel
	// under backoff — which re-runs register for a fresh im_token. Buffered so
	// the socket's OnError callback never blocks; only the first error matters.
	socketErr := make(chan error, 1)

	sock := c.newSocket(transport.SocketOptions{
		WSURL: reg.WSURL,
		UID:   reg.RobotID,
		Token: reg.IMToken,
		OnMessage: func(m transport.BotMessage) {
			c.onMessage(ctx, m)
		},
		OnError: func(e error) {
			c.logger.WarnContext(ctx, "octo: socket error", "robot_id", c.creds.RobotID, "error", e)
			select {
			case socketErr <- e:
			default:
			}
		},
		Logf: func(format string, args ...any) {
			c.logger.Debug(fmt.Sprintf(format, args...))
		},
	})
	if err := sock.Connect(ctx); err != nil {
		return fmt.Errorf("octo: open socket: %w", err)
	}
	defer sock.Disconnect()

	select {
	case <-ctx.Done():
		// Graceful teardown: the Supervisor cancelled the run context.
		return nil
	case err := <-socketErr:
		// The socket gave up reconnecting. ctx cancellation racing the error is a
		// graceful stop, not a failure.
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("octo: socket terminated: %w", err)
	}
}

// Disconnect is a no-op: the socket is torn down by ctx cancellation (the
// Supervisor cancels the run context), mirroring the other adapters.
func (c *octoChannel) Disconnect(ctx context.Context) error { return nil }

// Send posts a plain reply to the bound Octo channel. It exists to satisfy the
// channel.Channel interface, but Octo's real outbound path is the bus-driven
// Patcher (outbound.go), which sends with the exact WuKongIM channel type read
// back from the binding config — the engine never calls Send for Octo. Because
// channel.OutboundMessage does not carry the numeric channel type, Send can only
// assume a 1:1 DM; callers needing group/topic delivery must use the Patcher.
func (c *octoChannel) Send(ctx context.Context, out channel.OutboundMessage) (channel.SendResult, error) {
	hc := transport.NewHTTPClient(c.creds.APIURL, c.creds.BotToken)
	res, err := hc.SendMessage(ctx, transport.SendMessageParams{
		ChannelID:   out.ChatID,
		ChannelType: transport.ChannelDM,
		Content:     out.Text,
	})
	if err != nil {
		return channel.SendResult{}, fmt.Errorf("octo: send message: %w", err)
	}
	if res == nil {
		return channel.SendResult{}, nil
	}
	return channel.SendResult{MessageID: res.MessageID}, nil
}

// Capabilities declares what the Octo adapter supports today. Octo renders
// markdown natively but has no interactive cards, typing indicator, or message
// edit wired, so only CapText is declared. Declaration only — the engine
// performs no degradation.
func (c *octoChannel) Capabilities() channel.Capability {
	return channel.CapText
}

// ---- inbound ----

// onMessage bridges a decoded transport message to the engine handler. It drops
// non-user traffic before any dispatch work (the bot's own echoes; non-
// conversation system channels), strips the bot's own @mention, parses a leading
// /new directive, and normalizes the rest into a channel.InboundMessage. The
// Octo-specific fields the envelope does not carry (robot_id routing key, the
// numeric WuKongIM channel type) are stashed in Raw for the resolvers.
func (c *octoChannel) onMessage(ctx context.Context, m transport.BotMessage) {
	// 1. Drop the bot's own messages echoed back to its socket: without this,
	//    every outbound reply loops back as a new unbound-user message.
	if m.FromUID == c.creds.RobotID {
		return
	}
	// 2. Drop non-conversation channels (system/command channels Octo emits on
	//    connect) that otherwise slip past the group-mention gate.
	if !isConversationChannel(m.ChannelType) {
		return
	}

	body := stripBotMentions(m.Payload.Content, c.creds.RobotID, mentionEntities(m.Payload.Mention))
	forceFresh := false
	if cmd, ok := parseFreshSessionCommand(body); ok {
		forceFresh = true
		body = cmd.Body
	}

	chatType := channelChatType(m.ChannelType)
	raw, _ := json.Marshal(octoRawEvent{
		RobotID:     c.creds.RobotID,
		ChannelType: int16(m.ChannelType),
	})

	msg := channel.InboundMessage{
		EventID:        m.MessageID,
		MessageID:      m.MessageID,
		Type:           channel.MsgTypeText,
		Text:           body,
		AddressedToBot: addressedToBot(c.creds.RobotID, m),
		ForceFresh:     forceFresh,
		Source: channel.Source{
			ChannelType: TypeOcto,
			ChatID:      m.ChannelID,
			ChatType:    chatType,
			SenderID:    m.FromUID,
		},
		Raw: raw,
	}
	if err := c.handler(ctx, msg); err != nil {
		c.logger.ErrorContext(ctx, "octo: inbound handler failed",
			"robot_id", c.creds.RobotID, "error", err)
	}
}

// octoRawEvent carries the Octo-specific fields the cross-platform envelope does
// not — read back only inside the Octo resolvers (robot_id routes the
// installation; the numeric channel type drives outbound send). The core never
// reads Raw.
type octoRawEvent struct {
	RobotID     string `json:"robot_id"`
	ChannelType int16  `json:"channel_type"`
}

// isConversationChannel reports whether a channel type is a real user
// conversation (DM, group, or community topic). Octo also emits system/command
// channels (e.g. channel_type 8 "systemcmdonline" on connect) that must not be
// dispatched as user messages.
func isConversationChannel(t transport.ChannelType) bool {
	switch t {
	case transport.ChannelDM, transport.ChannelGroup, transport.ChannelTopic:
		return true
	default:
		return false
	}
}

// channelChatType maps a WuKongIM channel type to the normalized ChatType. Only
// a 1:1 DM is p2p; groups and community topics route through the engine's "must
// address the bot" group filter.
func channelChatType(t transport.ChannelType) channel.ChatType {
	if t == transport.ChannelDM {
		return channel.ChatTypeP2P
	}
	return channel.ChatTypeGroup
}

// addressedToBot reports whether a group message targets the bot (@mention).
// DMs are always addressed; for groups we check the mention uid list.
func addressedToBot(robotID string, m transport.BotMessage) bool {
	if m.ChannelType == transport.ChannelDM {
		return true
	}
	if m.Payload.Mention == nil {
		return false
	}
	return slices.Contains(m.Payload.Mention.UIDs, robotID)
}

// mentionEntities returns the entity list from a mention payload, or nil if the
// payload itself is nil.
func mentionEntities(m *transport.MentionPayload) []transport.MentionEntity {
	if m == nil {
		return nil
	}
	return m.Entities
}

// ---- registration ----

// OctoChannelDeps bundles the shared dependencies the Octo Factory closes over.
// The inbound handler is supplied per-build by the engine via
// channel.Config.Handler, mirroring FeishuChannelDeps / SlackChannelDeps.
type OctoChannelDeps struct {
	// Decrypt turns the stored bot token ciphertext into plaintext. A nil
	// Decrypter treats the stored token as plaintext (tests / un-encrypted dev).
	Decrypt Decrypter
	Logger  *slog.Logger
}

// RegisterOcto registers the Octo Factory on reg under TypeOcto so the
// engine.Supervisor can build an octoChannel per installation. "Adding a
// channel" is this call plus the adapter — no engine edit.
func RegisterOcto(reg *channel.Registry, deps OctoChannelDeps) {
	reg.Register(TypeOcto, newOctoFactory(deps))
}

func newOctoFactory(deps OctoChannelDeps) channel.Factory {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return func(cfg channel.Config) (channel.Channel, error) {
		creds, err := decodeCredentials(cfg.Raw, deps.Decrypt)
		if err != nil {
			return nil, err
		}
		if creds.BotToken == "" {
			return nil, errors.New("octo: installation config missing bot token")
		}
		if creds.APIURL == "" {
			return nil, errors.New("octo: installation config missing api_url")
		}
		return newOctoChannel(creds, cfg.Handler, logger), nil
	}
}

// newOctoChannel builds an octoChannel from decoded credentials. The socket and
// register seams default to the production transport; tests overwrite them.
func newOctoChannel(creds credentials, handler channel.InboundHandler, logger *slog.Logger) *octoChannel {
	if logger == nil {
		logger = slog.Default()
	}
	c := &octoChannel{creds: creds, handler: handler, logger: logger}
	c.newSocket = func(opts transport.SocketOptions) socketConn {
		return transport.NewSocket(opts)
	}
	c.register = func(ctx context.Context) (*transport.BotRegisterResp, error) {
		return transport.NewHTTPClient(creds.APIURL, creds.BotToken).Register(ctx, false, "Multica", "")
	}
	return c
}
