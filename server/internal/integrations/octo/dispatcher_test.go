package octo

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// --- fakes -----------------------------------------------------------------

type fakeQueries struct {
	inst       db.OctoInstallation
	instErr    error
	claimErr   error // returned by ClaimOctoInboundDedup (pgx.ErrNoRows = duplicate)
	binding    db.OctoUserBinding
	bindingErr error
	session    db.ChatSession
	sessionErr error

	markRows    int64
	releaseRows int64
	marked      bool
	released    bool
}

func (f *fakeQueries) GetOctoInstallationByRobotID(ctx context.Context, robotID string) (db.OctoInstallation, error) {
	return f.inst, f.instErr
}
func (f *fakeQueries) ClaimOctoInboundDedup(ctx context.Context, arg db.ClaimOctoInboundDedupParams) (db.OctoInboundDedup, error) {
	if f.claimErr != nil {
		return db.OctoInboundDedup{}, f.claimErr
	}
	return db.OctoInboundDedup{InstallationID: arg.InstallationID, MessageID: arg.MessageID, ClaimToken: validUUID(1)}, nil
}
func (f *fakeQueries) MarkOctoInboundDedupProcessed(ctx context.Context, arg db.MarkOctoInboundDedupProcessedParams) (int64, error) {
	f.marked = true
	return f.markRows, nil
}
func (f *fakeQueries) ReleaseOctoInboundDedup(ctx context.Context, arg db.ReleaseOctoInboundDedupParams) (int64, error) {
	f.released = true
	return f.releaseRows, nil
}
func (f *fakeQueries) GetOctoUserBindingByUID(ctx context.Context, arg db.GetOctoUserBindingByUIDParams) (db.OctoUserBinding, error) {
	return f.binding, f.bindingErr
}
func (f *fakeQueries) GetChatSession(ctx context.Context, id pgtype.UUID) (db.ChatSession, error) {
	return f.session, f.sessionErr
}

type fakeChat struct {
	sessionID    pgtype.UUID
	ensureErr    error
	appendResult AppendResult
	appendErr    error
}

func (f *fakeChat) EnsureChatSession(ctx context.Context, p EnsureChatSessionParams) (pgtype.UUID, error) {
	return f.sessionID, f.ensureErr
}
func (f *fakeChat) AppendUserMessage(ctx context.Context, p AppendUserMessageParams) (AppendResult, error) {
	return f.appendResult, f.appendErr
}

type fakeEnqueuer struct {
	task db.AgentTaskQueue
	err  error
	// called records whether EnqueueChatTask was invoked.
	called bool
}

func (f *fakeEnqueuer) EnqueueChatTask(ctx context.Context, session db.ChatSession, initiatorUserID pgtype.UUID) (db.AgentTaskQueue, error) {
	f.called = true
	return f.task, f.err
}

type fakeAudit struct {
	reasons []DropReason
}

func (f *fakeAudit) RecordDrop(ctx context.Context, p AuditDropParams) error {
	f.reasons = append(f.reasons, p.Reason)
	return nil
}

func validUUID(b byte) pgtype.UUID {
	var u pgtype.UUID
	for i := range u.Bytes {
		u.Bytes[i] = b
	}
	u.Valid = true
	return u
}

// activeInstallation returns a ready-to-route installation row.
func activeInstallation() db.OctoInstallation {
	return db.OctoInstallation{
		ID:              validUUID(0xAA),
		WorkspaceID:     validUUID(0xBB),
		AgentID:         validUUID(0xCC),
		InstallerUserID: validUUID(0xDD),
		RobotID:         "robot_x",
		Status:          "active",
	}
}

func boundUser() db.OctoUserBinding {
	return db.OctoUserBinding{
		ID:             validUUID(0xEE),
		MulticaUserID:  validUUID(0x11),
		InstallationID: validUUID(0xAA),
	}
}

func dmMessage() InboundMessage {
	return InboundMessage{
		RobotID:     "robot_x",
		MessageID:   "msg_1",
		SenderUID:   "uid_1",
		ChannelID:   "ch_1",
		ChannelType: ChannelDM,
		Body:        "hello",
	}
}

// newDispatcher wires a dispatcher over the supplied fakes.
func newDispatcher(q *fakeQueries, c *fakeChat, e *fakeEnqueuer, a *fakeAudit) *Dispatcher {
	return &Dispatcher{Queries: q, Chat: c, TaskService: e, Audit: a}
}

// --- tests -----------------------------------------------------------------

func TestHandle_UnknownRobot_Drops(t *testing.T) {
	q := &fakeQueries{instErr: pgx.ErrNoRows}
	a := &fakeAudit{}
	d := newDispatcher(q, &fakeChat{}, &fakeEnqueuer{}, a)

	res, err := d.Handle(context.Background(), dmMessage())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Outcome != OutcomeDropped || res.DropReason != DropReasonInvalidEvent {
		t.Errorf("got %+v, want dropped/invalid_event", res)
	}
}

func TestHandle_RevokedInstallation_Drops(t *testing.T) {
	inst := activeInstallation()
	inst.Status = "revoked"
	q := &fakeQueries{inst: inst}
	a := &fakeAudit{}
	d := newDispatcher(q, &fakeChat{}, &fakeEnqueuer{}, a)

	res, _ := d.Handle(context.Background(), dmMessage())
	if res.DropReason != DropReasonRevokedInstallation {
		t.Errorf("got %q, want revoked_installation", res.DropReason)
	}
}

func TestHandle_DuplicateClaim_Drops(t *testing.T) {
	q := &fakeQueries{inst: activeInstallation(), claimErr: pgx.ErrNoRows}
	a := &fakeAudit{}
	d := newDispatcher(q, &fakeChat{}, &fakeEnqueuer{}, a)

	res, _ := d.Handle(context.Background(), dmMessage())
	if res.DropReason != DropReasonDuplicate {
		t.Errorf("got %q, want duplicate", res.DropReason)
	}
}

func TestHandle_GroupNotAddressed_Drops(t *testing.T) {
	q := &fakeQueries{inst: activeInstallation(), markRows: 1}
	a := &fakeAudit{}
	d := newDispatcher(q, &fakeChat{}, &fakeEnqueuer{}, a)

	msg := dmMessage()
	msg.ChannelType = ChannelGroup
	msg.AddressedToBot = false

	res, _ := d.Handle(context.Background(), msg)
	if res.DropReason != DropReasonNotAddressedInGroup {
		t.Errorf("got %q, want not_addressed_in_group", res.DropReason)
	}
	if !q.marked {
		t.Errorf("expected dedup mark on a durable drop")
	}
}

func TestHandle_UnboundUser_NeedsBinding(t *testing.T) {
	q := &fakeQueries{inst: activeInstallation(), bindingErr: pgx.ErrNoRows, markRows: 1}
	a := &fakeAudit{}
	d := newDispatcher(q, &fakeChat{}, &fakeEnqueuer{}, a)

	res, _ := d.Handle(context.Background(), dmMessage())
	if res.Outcome != OutcomeNeedsBinding {
		t.Errorf("got %q, want needs_binding", res.Outcome)
	}
	if res.SenderUID != "uid_1" {
		t.Errorf("SenderUID = %q, want uid_1", res.SenderUID)
	}
}

func TestHandle_Ingested_EnqueuesTask(t *testing.T) {
	q := &fakeQueries{
		inst:    activeInstallation(),
		binding: boundUser(),
		session: db.ChatSession{ID: validUUID(0x22)},
	}
	c := &fakeChat{sessionID: validUUID(0x22), appendResult: AppendResult{DedupMarked: true}}
	e := &fakeEnqueuer{task: db.AgentTaskQueue{ID: validUUID(0x33)}}
	d := newDispatcher(q, c, e, &fakeAudit{})

	res, err := d.Handle(context.Background(), dmMessage())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Outcome != OutcomeIngested {
		t.Fatalf("got %q, want ingested", res.Outcome)
	}
	if !e.called {
		t.Errorf("expected EnqueueChatTask to be called")
	}
	if res.TaskID != validUUID(0x33) {
		t.Errorf("TaskID not propagated: %v", res.TaskID)
	}
	if q.marked || q.released {
		t.Errorf("ingest path should finalize in-tx (no post Mark/Release), got marked=%v released=%v", q.marked, q.released)
	}
}

func TestHandle_AgentOffline(t *testing.T) {
	q := &fakeQueries{
		inst:    activeInstallation(),
		binding: boundUser(),
		session: db.ChatSession{ID: validUUID(0x22)},
	}
	c := &fakeChat{sessionID: validUUID(0x22), appendResult: AppendResult{DedupMarked: true}}
	e := &fakeEnqueuer{err: service.ErrChatTaskAgentNoRuntime}
	d := newDispatcher(q, c, e, &fakeAudit{})

	res, _ := d.Handle(context.Background(), dmMessage())
	if res.Outcome != OutcomeAgentOffline {
		t.Errorf("got %q, want agent_offline", res.Outcome)
	}
}

func TestHandle_AgentArchived(t *testing.T) {
	q := &fakeQueries{
		inst:    activeInstallation(),
		binding: boundUser(),
		session: db.ChatSession{ID: validUUID(0x22)},
	}
	c := &fakeChat{sessionID: validUUID(0x22), appendResult: AppendResult{DedupMarked: true}}
	e := &fakeEnqueuer{err: service.ErrChatTaskAgentArchived}
	d := newDispatcher(q, c, e, &fakeAudit{})

	res, _ := d.Handle(context.Background(), dmMessage())
	if res.Outcome != OutcomeAgentArchived {
		t.Errorf("got %q, want agent_archived", res.Outcome)
	}
}

func TestHandle_ClaimLost_DropsDuplicate(t *testing.T) {
	q := &fakeQueries{
		inst:    activeInstallation(),
		binding: boundUser(),
	}
	c := &fakeChat{sessionID: validUUID(0x22), appendErr: ErrClaimLost}
	d := newDispatcher(q, c, &fakeEnqueuer{}, &fakeAudit{})

	res, err := d.Handle(context.Background(), dmMessage())
	if err != nil {
		t.Fatalf("ErrClaimLost should be swallowed as a drop, got err: %v", err)
	}
	if res.DropReason != DropReasonDuplicate {
		t.Errorf("got %q, want duplicate", res.DropReason)
	}
}

func TestHandle_EnsureSessionError_Releases(t *testing.T) {
	q := &fakeQueries{
		inst:        activeInstallation(),
		binding:     boundUser(),
		releaseRows: 1,
	}
	c := &fakeChat{ensureErr: errors.New("db down")}
	d := newDispatcher(q, c, &fakeEnqueuer{}, &fakeAudit{})

	_, err := d.Handle(context.Background(), dmMessage())
	if err == nil {
		t.Fatalf("expected infra error")
	}
	if !q.released {
		t.Errorf("expected dedup release on pre-durable failure")
	}
}
