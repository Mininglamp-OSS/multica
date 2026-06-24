package octo

import (
	"sort"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/multica-ai/multica/server/internal/integrations/octo/transport"
)

// stripBotMentions removes every occurrence of the bot's own @mention from
// content, using the offset/length entries the WuKongIM server attaches in
// MentionEntity. A single adjacent space (after the mention, or before if
// there is no trailing space) is also trimmed so downstream first-line
// directive parsers (e.g. /new) see a clean leading body — without this,
// group/topic messages arrive as "@<bot> /new ..." and a strict-prefix
// match never fires.
//
// Mentions of OTHER uids are left intact: the agent should still see who
// else the user invoked. This mirrors Lark's `resolveMentions`, which only
// strips the bot's own placeholder.
//
// content's offsets are in UTF-16 code units (per MentionEntity's doc and
// the JS wire format), but Go strings are UTF-8. We walk the runes once,
// translating each entity's UTF-16 range to a byte range, then excise the
// matching ranges right-to-left so earlier byte indices remain valid.
//
// Defensive against malformed input: entities whose range falls outside
// content (or straddles a surrogate boundary) are skipped silently rather
// than producing a corrupted string. An empty content, empty robotID, or
// nil/empty entities slice short-circuits to a verbatim return.
func stripBotMentions(content, robotID string, entities []transport.MentionEntity) string {
	if content == "" || robotID == "" || len(entities) == 0 {
		return content
	}
	type byteRange struct{ start, end int }
	var ranges []byteRange
	for _, e := range entities {
		if e.UID != robotID || e.Length <= 0 || e.Offset < 0 {
			continue
		}
		startByte, ok := utf16OffsetToByte(content, e.Offset)
		if !ok {
			continue
		}
		endByte, ok := utf16OffsetToByte(content, e.Offset+e.Length)
		if !ok {
			continue
		}
		ranges = append(ranges, byteRange{startByte, endByte})
	}
	if len(ranges) == 0 {
		return content
	}
	// Process right-to-left so excising one range does not shift the byte
	// indices of ranges still to be excised.
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start > ranges[j].start })
	for _, r := range ranges {
		end := r.end
		start := r.start
		// Trim one adjacent space — prefer trailing ("@bot foo" → "foo");
		// fall back to leading ("foo @bot" → "foo"). Mirrors Lark.
		if end < len(content) && content[end] == ' ' {
			end++
		} else if start > 0 && content[start-1] == ' ' {
			start--
		}
		content = content[:start] + content[end:]
	}
	return content
}

// utf16OffsetToByte translates a UTF-16 code-unit offset into a byte offset
// within s (a UTF-8 string). Returns (offset, true) when the boundary is
// well-defined (or at the end of the string), and (0, false) when the offset
// falls inside a surrogate pair or past the end. Used by stripBotMentions
// to convert wire-format MentionEntity ranges into Go slice bounds.
func utf16OffsetToByte(s string, utf16Offset int) (int, bool) {
	if utf16Offset == 0 {
		return 0, true
	}
	byteIdx := 0
	utf16Idx := 0
	for byteIdx < len(s) {
		if utf16Idx == utf16Offset {
			return byteIdx, true
		}
		if utf16Idx > utf16Offset {
			// Offset lands inside a surrogate pair — the wire ranges are
			// supposed to align to user-perceived character boundaries, so
			// refuse rather than silently round.
			return 0, false
		}
		r, size := utf8.DecodeRuneInString(s[byteIdx:])
		if r == utf8.RuneError && size == 1 {
			// Invalid UTF-8 byte — count as one code unit and advance.
			utf16Idx++
		} else {
			rl := utf16.RuneLen(r)
			if rl < 0 {
				rl = 1 // unreachable for valid runes; defensive
			}
			utf16Idx += rl
		}
		byteIdx += size
	}
	if utf16Idx == utf16Offset {
		return byteIdx, true
	}
	return 0, false
}
