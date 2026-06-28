package octo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// InstallationParams carries the inputs to create or update an Octo bot
// installation. BotToken is the plaintext bf_* token; it is encrypted at rest
// via secretbox and stored (base64) in channel_installation.config, never in the
// clear.
type InstallationParams struct {
	WorkspaceID     pgtype.UUID
	AgentID         pgtype.UUID
	BotToken        string
	RobotID         string
	BotName         string
	OwnerUID        string
	APIURL          string
	WSURL           string
	InstallerUserID pgtype.UUID
}

// InstallationService manages the Octo channel_installation rows (channel_type=
// 'octo'), encrypting the bot token at rest with a secretbox.Box. It also
// satisfies the outbound TokenDecryptor interface (DecryptBotToken), decoding the
// token back out of the config blob.
type InstallationService struct {
	queries *db.Queries
	box     *secretbox.Box
}

// NewInstallationService constructs the service. The box MUST be non-nil; the
// whole Octo integration is gated on a configured MULTICA_OCTO_SECRET_KEY, so a
// nil box is a programming error rather than a degraded mode.
func NewInstallationService(queries *db.Queries, box *secretbox.Box) (*InstallationService, error) {
	if box == nil {
		return nil, errors.New("octo: InstallationService requires a non-nil secretbox.Box")
	}
	return &InstallationService{queries: queries, box: box}, nil
}

// Upsert creates or refreshes the (workspace, agent) Octo installation, sealing
// the bot token into the config blob before write.
func (s *InstallationService) Upsert(ctx context.Context, p InstallationParams) (db.ChannelInstallation, error) {
	if err := validateInstallationParams(p); err != nil {
		return db.ChannelInstallation{}, err
	}
	sealed, err := s.box.Seal([]byte(p.BotToken))
	if err != nil {
		return db.ChannelInstallation{}, fmt.Errorf("seal bot token: %w", err)
	}
	config, err := encodeConfig(p, sealed)
	if err != nil {
		return db.ChannelInstallation{}, fmt.Errorf("encode config: %w", err)
	}
	inst, err := s.queries.UpsertChannelInstallation(ctx, db.UpsertChannelInstallationParams{
		WorkspaceID:     p.WorkspaceID,
		AgentID:         p.AgentID,
		ChannelType:     string(TypeOcto),
		Config:          config,
		InstallerUserID: p.InstallerUserID,
	})
	if err != nil {
		// The upsert's ON CONFLICT covers (workspace_id, agent_id, channel_type).
		// Binding a bot whose robot_id is already in use by a DIFFERENT agent
		// trips the (channel_type, config->>'app_id') unique index
		// (idx_channel_installation_type_appid, 23505). Surface that as a typed
		// error so the handler returns 409 instead of a generic 500.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "idx_channel_installation_type_appid" {
			return db.ChannelInstallation{}, ErrRobotAlreadyBound
		}
		return db.ChannelInstallation{}, err
	}
	return inst, nil
}

// ErrRobotAlreadyBound is returned by Upsert when the bot's robot_id is already
// bound to a different agent (the per-(channel_type, app_id) unique index is
// deployment-wide: one Octo bot maps to exactly one Multica agent). Translated to
// 409 at the HTTP boundary. The fix is to revoke the existing installation first.
var ErrRobotAlreadyBound = errors.New("octo bot is already bound to another agent")

// Revoke marks an installation revoked; the engine Supervisor tears down its WS
// on the next sweep.
func (s *InstallationService) Revoke(ctx context.Context, id pgtype.UUID) error {
	return s.queries.SetChannelInstallationStatus(ctx, db.SetChannelInstallationStatusParams{
		ID:     id,
		Status: string(InstallationRevoked),
	})
}

// DecryptBotToken returns the plaintext bot token for an installation, decoding
// it from the config blob. It satisfies the outbound TokenDecryptor interface.
func (s *InstallationService) DecryptBotToken(inst db.ChannelInstallation) (string, error) {
	creds, err := decodeCredentials(inst.Config, s.box.Open)
	if err != nil {
		return "", err
	}
	return creds.BotToken, nil
}

// GetInWorkspace loads a workspace-scoped Octo installation (HTTP handler path).
// Returns ErrInstallationNotFound when no matching row exists.
func (s *InstallationService) GetInWorkspace(ctx context.Context, id, workspaceID pgtype.UUID) (db.ChannelInstallation, error) {
	inst, err := s.queries.GetChannelInstallationInWorkspace(ctx, db.GetChannelInstallationInWorkspaceParams{
		ID:          id,
		WorkspaceID: workspaceID,
		ChannelType: string(TypeOcto),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.ChannelInstallation{}, ErrInstallationNotFound
	}
	return inst, err
}

// ErrInstallationNotFound is returned by GetInWorkspace when no matching
// installation row exists for the (id, workspace) pair.
var ErrInstallationNotFound = errors.New("octo installation not found")

// ListByWorkspace lists a workspace's Octo installations (HTTP handler path).
func (s *InstallationService) ListByWorkspace(ctx context.Context, workspaceID pgtype.UUID) ([]db.ChannelInstallation, error) {
	return s.queries.ListChannelInstallationsByWorkspace(ctx, db.ListChannelInstallationsByWorkspaceParams{
		WorkspaceID: workspaceID,
		ChannelType: string(TypeOcto),
	})
}

func validateInstallationParams(p InstallationParams) error {
	switch {
	case !p.WorkspaceID.Valid:
		return errors.New("octo: installation requires workspace_id")
	case !p.AgentID.Valid:
		return errors.New("octo: installation requires agent_id")
	case p.BotToken == "":
		return errors.New("octo: installation requires a bot token")
	case p.RobotID == "":
		return errors.New("octo: installation requires robot_id")
	case p.APIURL == "":
		return errors.New("octo: installation requires api_url")
	case !p.InstallerUserID.Valid:
		return errors.New("octo: installation requires installer_user_id")
	}
	return nil
}
