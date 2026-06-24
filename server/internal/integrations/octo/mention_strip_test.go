package octo

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/integrations/octo/transport"
)

func TestStripBotMentions(t *testing.T) {
	const bot = "robot_x"
	tests := []struct {
		name     string
		content  string
		robot    string
		entities []transport.MentionEntity
		want     string
	}{
		{
			name:    "no content",
			content: "",
			robot:   bot,
			want:    "",
		},
		{
			name:    "no entities",
			content: "@<bot> /new restart",
			robot:   bot,
			want:    "@<bot> /new restart",
		},
		{
			name:    "no matching entity",
			content: "@alice hi",
			robot:   bot,
			entities: []transport.MentionEntity{
				{UID: "uid_alice", Offset: 0, Length: 6},
			},
			want: "@alice hi",
		},
		{
			name:    "leading bot mention strips with trailing space",
			content: "@<bot> /new restart",
			robot:   bot,
			entities: []transport.MentionEntity{
				{UID: bot, Offset: 0, Length: 6},
			},
			want: "/new restart",
		},
		{
			name:    "trailing bot mention strips leading space",
			content: "do this @<bot>",
			robot:   bot,
			entities: []transport.MentionEntity{
				{UID: bot, Offset: 8, Length: 6},
			},
			want: "do this",
		},
		{
			name:    "mid-string bot mention strips trailing space",
			content: "hello @<bot> world",
			robot:   bot,
			entities: []transport.MentionEntity{
				{UID: bot, Offset: 6, Length: 6},
			},
			want: "hello world",
		},
		{
			name:    "two bot mentions both stripped",
			content: "@<bot> ping @<bot>",
			robot:   bot,
			entities: []transport.MentionEntity{
				{UID: bot, Offset: 0, Length: 6},
				{UID: bot, Offset: 12, Length: 6},
			},
			want: "ping",
		},
		{
			name:    "other user mention preserved alongside stripped bot",
			content: "@<bot> @alice tell her",
			robot:   bot,
			entities: []transport.MentionEntity{
				{UID: bot, Offset: 0, Length: 6},
				{UID: "uid_alice", Offset: 7, Length: 6},
			},
			want: "@alice tell her",
		},
		{
			name:    "surrogate-pair emoji before mention shifts byte offset",
			content: "👋 @<bot> hi",
			robot:   bot,
			// "👋" is 1 rune, 2 UTF-16 code units (surrogate pair), 4 UTF-8 bytes.
			// " " is 1 rune, 1 UTF-16, 1 byte.
			// "@<bot>" starts at UTF-16 offset 3, length 6.
			entities: []transport.MentionEntity{
				{UID: bot, Offset: 3, Length: 6},
			},
			want: "👋 hi",
		},
		{
			name:    "out-of-bounds entity ignored",
			content: "/new restart",
			robot:   bot,
			entities: []transport.MentionEntity{
				{UID: bot, Offset: 100, Length: 6},
			},
			want: "/new restart",
		},
		{
			name:    "zero-length entity ignored",
			content: "@<bot> hi",
			robot:   bot,
			entities: []transport.MentionEntity{
				{UID: bot, Offset: 0, Length: 0},
			},
			want: "@<bot> hi",
		},
		{
			name:    "empty robot id no-ops even with entity",
			content: "@<bot> hi",
			robot:   "",
			entities: []transport.MentionEntity{
				{UID: bot, Offset: 0, Length: 6},
			},
			want: "@<bot> hi",
		},
		{
			name:    "bot mention with no adjacent spaces leaves remainder",
			content: "@<bot>",
			robot:   bot,
			entities: []transport.MentionEntity{
				{UID: bot, Offset: 0, Length: 6},
			},
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stripBotMentions(tc.content, tc.robot, tc.entities)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUTF16OffsetToByte(t *testing.T) {
	tests := []struct {
		name        string
		s           string
		utf16Offset int
		wantByte    int
		wantOK      bool
	}{
		{name: "zero offset on empty string", s: "", utf16Offset: 0, wantByte: 0, wantOK: true},
		{name: "ascii start", s: "hello", utf16Offset: 0, wantByte: 0, wantOK: true},
		{name: "ascii middle", s: "hello", utf16Offset: 3, wantByte: 3, wantOK: true},
		{name: "ascii end", s: "hello", utf16Offset: 5, wantByte: 5, wantOK: true},
		{name: "ascii past end", s: "hello", utf16Offset: 6, wantByte: 0, wantOK: false},
		// 中 = 1 rune, 1 UTF-16 code unit, 3 UTF-8 bytes
		{name: "bmp cjk start", s: "中文", utf16Offset: 0, wantByte: 0, wantOK: true},
		{name: "bmp cjk after one rune", s: "中文", utf16Offset: 1, wantByte: 3, wantOK: true},
		{name: "bmp cjk after both runes", s: "中文", utf16Offset: 2, wantByte: 6, wantOK: true},
		// 👋 = 1 rune, 2 UTF-16 code units (surrogate pair), 4 UTF-8 bytes
		{name: "surrogate pair past start", s: "👋x", utf16Offset: 2, wantByte: 4, wantOK: true},
		{name: "surrogate pair after x", s: "👋x", utf16Offset: 3, wantByte: 5, wantOK: true},
		{name: "surrogate pair mid (rejected)", s: "👋x", utf16Offset: 1, wantByte: 0, wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := utf16OffsetToByte(tc.s, tc.utf16Offset)
			if ok != tc.wantOK || got != tc.wantByte {
				t.Errorf("got (%d, %v), want (%d, %v)", got, ok, tc.wantByte, tc.wantOK)
			}
		})
	}
}
