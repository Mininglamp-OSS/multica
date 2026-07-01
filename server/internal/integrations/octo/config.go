package octo

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// installConfig is the JSON shape stored in channel_installation.config for an
// Octo installation. The cross-platform columns stay flat; everything
// Octo-specific lives in this opaque blob (the documented config boundary,
// mirroring slack.installConfig).
//
// app_id holds the Octo robot_id — the per-installation routing key — so the
// generic GetChannelInstallationByAppID query (which reads config->>'app_id')
// and the (channel_type, config->>'app_id') unique index route Octo inbound
// events with NO new query and NO schema change. robot_id is also kept as its
// own field for readability; the two carry the same value.
//
// The bot token (bf_*) is stored as base64-encoded secretbox ciphertext (never
// plaintext), mirroring Feishu's app_secret_encrypted and Slack's
// bot_token_encrypted. api_url / ws_url are the Octo REST/WS endpoints cached
// from the bot register response.
type installConfig struct {
	AppID             string `json:"app_id"`
	RobotID           string `json:"robot_id,omitempty"`
	APIURL            string `json:"api_url"`
	WSURL             string `json:"ws_url,omitempty"`
	BotName           string `json:"bot_name,omitempty"`
	OwnerUID          string `json:"owner_uid,omitempty"`
	BotTokenEncrypted string `json:"bot_token_encrypted"`
}

// credentials is the decoded, decrypted form the adapter runs on. The
// installation IDENTITY (workspace / agent / installer) is deliberately absent:
// it is resolved per message by the Router's InstallationResolver, exactly as
// the Feishu and Slack adapters do.
type credentials struct {
	RobotID  string
	APIURL   string
	WSURL    string
	BotName  string
	OwnerUID string
	BotToken string
}

// Decrypter turns stored ciphertext into plaintext. The wiring injects a
// secretbox-backed implementation; tests inject an identity decrypter (or nil,
// which treats the stored bytes as plaintext).
type Decrypter func(ciphertext []byte) (plaintext []byte, err error)

// decodeCredentials parses the per-installation config blob and decrypts the
// stored bot token. It is the single place the Octo config JSON is interpreted.
func decodeCredentials(raw json.RawMessage, decrypt Decrypter) (credentials, error) {
	if len(raw) == 0 {
		return credentials{}, errors.New("octo: empty installation config")
	}
	var cfg installConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return credentials{}, fmt.Errorf("decode octo installation config: %w", err)
	}
	botToken, err := decryptToken(cfg.BotTokenEncrypted, decrypt)
	if err != nil {
		return credentials{}, fmt.Errorf("decrypt bot token: %w", err)
	}
	robotID := cfg.RobotID
	if robotID == "" {
		robotID = cfg.AppID
	}
	return credentials{
		RobotID:  robotID,
		APIURL:   cfg.APIURL,
		WSURL:    cfg.WSURL,
		BotName:  cfg.BotName,
		OwnerUID: cfg.OwnerUID,
		BotToken: botToken,
	}, nil
}

// encodeConfig builds the channel_installation.config blob from installation
// inputs, sealing the bot token before storage. The sealed bytes are
// base64-encoded so the JSONB column carries valid text (PostgreSQL's
// encode(...,'base64') round-trips with this).
func encodeConfig(p InstallationParams, sealed []byte) (json.RawMessage, error) {
	return json.Marshal(installConfig{
		AppID:             p.RobotID,
		RobotID:           p.RobotID,
		APIURL:            p.APIURL,
		WSURL:             p.WSURL,
		BotName:           p.BotName,
		OwnerUID:          p.OwnerUID,
		BotTokenEncrypted: base64.StdEncoding.EncodeToString(sealed),
	})
}

// decryptToken base64-decodes the stored ciphertext (tolerating the MIME
// newline wrapping PostgreSQL's encode(...,'base64') emits) and runs it through
// the injected Decrypter. An empty stored value decodes to an empty token; a
// nil Decrypter treats the decoded bytes as plaintext (test convenience).
func decryptToken(enc string, decrypt Decrypter) (string, error) {
	if enc == "" {
		return "", nil
	}
	ciphertext, err := base64.StdEncoding.DecodeString(stripWhitespace(enc))
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	if decrypt == nil {
		return string(ciphertext), nil
	}
	plaintext, err := decrypt(ciphertext)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// stripWhitespace removes ASCII whitespace so a MIME-wrapped base64 string
// (newlines every 64 chars) and an unwrapped one decode identically.
func stripWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r':
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
