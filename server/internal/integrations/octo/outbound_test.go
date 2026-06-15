package octo

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/integrations/octo/transport"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type fakePatcherQueries struct {
	binding    db.OctoChatSessionBinding
	bindingErr error
	inst       db.OctoInstallation
	instErr    error
	recorded   *db.CreateOctoOutboundMessageParams
}

func (f *fakePatcherQueries) GetOctoChatSessionBindingBySession(ctx context.Context, id pgtype.UUID) (db.OctoChatSessionBinding, error) {
	return f.binding, f.bindingErr
}
func (f *fakePatcherQueries) GetOctoInstallation(ctx context.Context, id pgtype.UUID) (db.OctoInstallation, error) {
	return f.inst, f.instErr
}
func (f *fakePatcherQueries) CreateOctoOutboundMessage(ctx context.Context, arg db.CreateOctoOutboundMessageParams) (db.OctoOutboundMessage, error) {
	f.recorded = &arg
	return db.OctoOutboundMessage{}, nil
}

type fakeDecryptor struct {
	token string
	err   error
}

func (f fakeDecryptor) DecryptBotToken(inst db.OctoInstallation) (string, error) {
	return f.token, f.err
}

type fakeSender struct {
	sent    int
	lastTxt string
	res     *transport.SendMessageResult
	err     error
}

func (f *fakeSender) Send(ctx context.Context, apiURL, botToken, channelID string, channelType transport.ChannelType, content string) (*transport.SendMessageResult, error) {
	f.sent++
	f.lastTxt = content
	if f.res == nil {
		f.res = &transport.SendMessageResult{MessageID: "m1", MessageSeq: 5}
	}
	return f.res, f.err
}

func activeInst() db.OctoInstallation {
	return db.OctoInstallation{ID: validUUID(0xAA), Status: "active", ApiUrl: "https://im.example/api"}
}

func octoBinding() db.OctoChatSessionBinding {
	return db.OctoChatSessionBinding{
		ChatSessionID:   validUUID(0x22),
		InstallationID:  validUUID(0xAA),
		OctoChannelID:   "ch_1",
		OctoChannelType: 1,
	}
}

func chatDoneEvent(content string) events.Event {
	return events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        "11111111-1111-1111-1111-111111111111",
		ChatSessionID: "22222222-2222-2222-2222-222222222222",
		Payload:       protocol.ChatDonePayload{Content: content},
	}
}

func newPatcher(q *fakePatcherQueries, s *fakeSender) *Patcher {
	return NewPatcher(q, fakeDecryptor{token: "bf_x"}, s, nil)
}

func TestProcessEvent_ChatDone_SendsReply(t *testing.T) {
	q := &fakePatcherQueries{binding: octoBinding(), inst: activeInst()}
	s := &fakeSender{}
	p := newPatcher(q, s)

	if err := p.processEvent(context.Background(), chatDoneEvent("hello world")); err != nil {
		t.Fatalf("processEvent: %v", err)
	}
	if s.sent != 1 || s.lastTxt != "hello world" {
		t.Errorf("sent=%d lastTxt=%q", s.sent, s.lastTxt)
	}
	if q.recorded == nil || q.recorded.OctoMessageID != "m1" {
		t.Errorf("expected outbound message recorded with id m1, got %+v", q.recorded)
	}
}

func TestProcessEvent_TaskFailed_SendsError(t *testing.T) {
	q := &fakePatcherQueries{binding: octoBinding(), inst: activeInst()}
	s := &fakeSender{}
	p := newPatcher(q, s)

	e := events.Event{
		Type:          protocol.EventTaskFailed,
		TaskID:        "11111111-1111-1111-1111-111111111111",
		ChatSessionID: "22222222-2222-2222-2222-222222222222",
		Payload:       map[string]any{"error": "boom"},
	}
	if err := p.processEvent(context.Background(), e); err != nil {
		t.Fatalf("processEvent: %v", err)
	}
	if s.sent != 1 || s.lastTxt != "⚠️ boom" {
		t.Errorf("sent=%d lastTxt=%q, want error text", s.sent, s.lastTxt)
	}
}

func TestProcessEvent_TaskFailed_FallsBackToFailureReason(t *testing.T) {
	q := &fakePatcherQueries{binding: octoBinding(), inst: activeInst()}
	s := &fakeSender{}
	p := newPatcher(q, s)

	// No explicit error string — only the coarse classifier. The relay must
	// translate it into the friendly Chinese copy, not the generic fallback.
	e := events.Event{
		Type:          protocol.EventTaskFailed,
		TaskID:        "11111111-1111-1111-1111-111111111111",
		ChatSessionID: "22222222-2222-2222-2222-222222222222",
		Payload:       map[string]any{"failure_reason": "agent_error.provider_auth_or_access"},
	}
	if err := p.processEvent(context.Background(), e); err != nil {
		t.Fatalf("processEvent: %v", err)
	}
	want := "⚠️ " + failureReasonText["agent_error.provider_auth_or_access"]
	if s.sent != 1 || s.lastTxt != want {
		t.Errorf("sent=%d lastTxt=%q, want %q", s.sent, s.lastTxt, want)
	}
}

func TestProcessEvent_TaskFailed_DefaultWhenNoDetail(t *testing.T) {
	q := &fakePatcherQueries{binding: octoBinding(), inst: activeInst()}
	s := &fakeSender{}
	p := newPatcher(q, s)

	// Neither error nor failure_reason — the user still gets an actionable,
	// non-empty message rather than a bare or English fallback.
	e := events.Event{
		Type:          protocol.EventTaskFailed,
		TaskID:        "11111111-1111-1111-1111-111111111111",
		ChatSessionID: "22222222-2222-2222-2222-222222222222",
		Payload:       map[string]any{},
	}
	if err := p.processEvent(context.Background(), e); err != nil {
		t.Fatalf("processEvent: %v", err)
	}
	if s.sent != 1 || s.lastTxt != "⚠️ "+defaultFailureMessage {
		t.Errorf("sent=%d lastTxt=%q, want default", s.sent, s.lastTxt)
	}
}

func TestFailureMessageFromPayload(t *testing.T) {
	cases := []struct {
		name    string
		payload any
		want    string
	}{
		{"explicit error wins", map[string]any{"error": "boom", "failure_reason": "timeout"}, "boom"},
		{"error_message alias", map[string]any{"error_message": "kaboom"}, "kaboom"},
		{"known reason", map[string]any{"failure_reason": "runtime_offline"}, failureReasonText["runtime_offline"]},
		{"unknown reason downgrades", map[string]any{"failure_reason": "some_future_reason"}, defaultFailureMessage},
		{"empty map", map[string]any{}, defaultFailureMessage},
		{"non-map payload", "not a map", defaultFailureMessage},
		{"nil payload", nil, defaultFailureMessage},
		{"empty error falls through to reason", map[string]any{"error": "", "failure_reason": "timeout"}, failureReasonText["timeout"]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := failureMessageFromPayload(tc.payload); got != tc.want {
				t.Errorf("failureMessageFromPayload(%v) = %q, want %q", tc.payload, got, tc.want)
			}
		})
	}
}

func TestProcessEvent_WebOnlySession_Skips(t *testing.T) {
	q := &fakePatcherQueries{bindingErr: pgx.ErrNoRows}
	s := &fakeSender{}
	p := newPatcher(q, s)

	if err := p.processEvent(context.Background(), chatDoneEvent("hi")); err != nil {
		t.Fatalf("processEvent: %v", err)
	}
	if s.sent != 0 {
		t.Errorf("web-only session should not send, sent=%d", s.sent)
	}
}

func TestProcessEvent_RevokedInstallation_Skips(t *testing.T) {
	inst := activeInst()
	inst.Status = "revoked"
	q := &fakePatcherQueries{binding: octoBinding(), inst: inst}
	s := &fakeSender{}
	p := newPatcher(q, s)

	if err := p.processEvent(context.Background(), chatDoneEvent("hi")); err != nil {
		t.Fatalf("processEvent: %v", err)
	}
	if s.sent != 0 {
		t.Errorf("revoked installation should not send, sent=%d", s.sent)
	}
}

func TestProcessEvent_EmptyContent_Dropped(t *testing.T) {
	q := &fakePatcherQueries{binding: octoBinding(), inst: activeInst()}
	s := &fakeSender{}
	p := newPatcher(q, s)

	if err := p.processEvent(context.Background(), chatDoneEvent("")); err != nil {
		t.Fatalf("processEvent: %v", err)
	}
	if s.sent != 0 {
		t.Errorf("empty content should be dropped, sent=%d", s.sent)
	}
}

func TestProcessEvent_NoChatSession_Skips(t *testing.T) {
	q := &fakePatcherQueries{}
	s := &fakeSender{}
	p := newPatcher(q, s)

	// Issue task: task_id present, no chat_session_id.
	e := events.Event{Type: protocol.EventTaskFailed, TaskID: "11111111-1111-1111-1111-111111111111", Payload: map[string]any{"error": "x"}}
	if err := p.processEvent(context.Background(), e); err != nil {
		t.Fatalf("processEvent: %v", err)
	}
	if s.sent != 0 {
		t.Errorf("no chat session should skip, sent=%d", s.sent)
	}
}

func TestProcessEvent_SendError_Propagates(t *testing.T) {
	q := &fakePatcherQueries{binding: octoBinding(), inst: activeInst()}
	s := &fakeSender{err: errors.New("network down")}
	p := newPatcher(q, s)

	if err := p.processEvent(context.Background(), chatDoneEvent("hi")); err == nil {
		t.Errorf("expected send error to propagate")
	}
}
