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
// Hardening against the ingress path's untrusted payloads — this function
// runs inside the WS socket read goroutine (hub.go's OnMessage path) with
// no recover above it, so any panic here kills every installation the
// process serves. Three defenses:
//
//   - Entities whose UTF-16 range falls outside content (or straddles a
//     surrogate boundary) are skipped silently rather than producing a
//     corrupted string.
//   - Overlapping ranges (e.g. duplicate or malformed entities both tagged
//     with the bot UID) are merged into their union before excision via an
//     ascending sort + collapse-if-touching pass; without this, the
//     rightmost excise shrinks content and a later overlapping range would
//     slice past the new end. See the PR #46 review thread for the
//     original repro (`[{0,9},{6,6}]` on `"@<bot>@<bot> hi"`).
//   - Each surviving range is bounds-checked at excise time as a final
//     belt-and-suspenders guard.
//
// Empty content, empty robotID, or a nil/empty entities slice short-
// circuits to a verbatim return.
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
	// Merge overlapping ranges: sort ascending by start, then collapse each
	// range that overlaps or abuts the previous kept range. A well-behaved
	// WuKongIM payload has at most one bot-mention span per region, so this
	// is purely a robustness step against malformed/duplicate inputs — but
	// the alternative (no merge) crashes the hub goroutine, and a plain
	// "drop overlapping" would leave dangling text from the dropped range.
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	merged := make([]byteRange, 0, len(ranges))
	for _, r := range ranges {
		if len(merged) > 0 && r.start <= merged[len(merged)-1].end {
			if r.end > merged[len(merged)-1].end {
				merged[len(merged)-1].end = r.end
			}
			continue
		}
		merged = append(merged, r)
	}
	// Excise right-to-left so earlier byte indices stay valid as content
	// shrinks. The bounds guard inside the loop is defense-in-depth: after
	// merge the ranges should always be valid against `content`, but on the
	// ingress path "always" is not strong enough.
	sort.Slice(merged, func(i, j int) bool { return merged[i].start > merged[j].start })
	for _, r := range merged {
		start, end := r.start, r.end
		if start < 0 || end > len(content) || start > end {
			continue
		}
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
