// Package chunk splits handbook Markdown into structure-aware chunks (proposal.md §4.1).
package chunk

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// maxChars is the soft size budget for one chunk body (~400 tokens).
// Tables are never split, so a table may exceed it.
const maxChars = 1600

var (
	headingRE = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)
	listRE    = regexp.MustCompile(`^\s*([-*+]|\d+[.)])\s`)
)

// Chunk is one retrievable passage. Content already carries the
// "Document title > Heading path" prefix so isolated passages stay interpretable.
type Chunk struct {
	ID          string   // stable across re-ingestion of unchanged structure
	Ordinal     int      // position within the document
	HeadingPath []string // H2+ path; the first H1 is the document title
	Content     string
	Type        string // prose | table | list
}

type block struct {
	kind  string
	lines []string
}

// text joins the block's lines back into one string.
func (b block) text() string { return strings.Join(b.lines, "\n") }

type section struct {
	path   []string
	blocks []block
}

// Split chunks one document body (front matter already removed).
// The first H1 is treated as the document title; it is not repeated in heading paths.
func Split(sourceID, title, body string) []Chunk {
	occurrences := map[string]int{}
	var out []Chunk
	for _, sec := range parseSections(body) {
		key := strings.Join(sec.path, "\x1f")
		occ := occurrences[key]
		occurrences[key]++
		for _, c := range packSection(sourceID, title, sec, occ) {
			c.Ordinal = len(out)
			out = append(out, c)
		}
	}
	return out
}

// parseSections splits a Markdown body into sections, each carrying the heading path it sits under. The first H1 is the document title and is skipped.
func parseSections(body string) []section {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")

	type heading struct {
		level int
		text  string
	}
	var stack []heading
	seenTitle := false

	var sections []section
	cur := section{}
	var para []string

	flushBlock := func() {
		if len(para) > 0 {
			cur.blocks = append(cur.blocks, newBlock(para))
			para = nil
		}
	}
	flushSection := func() {
		flushBlock()
		if len(cur.blocks) > 0 {
			sections = append(sections, cur)
		}
	}

	for _, line := range lines {
		if m := headingRE.FindStringSubmatch(line); m != nil {
			level := len(m[1])
			if level == 1 && !seenTitle {
				seenTitle = true
				continue
			}
			flushSection()
			for len(stack) > 0 && stack[len(stack)-1].level >= level {
				stack = stack[:len(stack)-1]
			}
			stack = append(stack, heading{level: level, text: m[2]})
			path := make([]string, 0, len(stack))
			for _, h := range stack {
				path = append(path, h.text)
			}
			cur = section{path: path}
			continue
		}
		if strings.TrimSpace(line) == "" {
			flushBlock()
			continue
		}
		// A table boundary always starts a new block so tables stay isolated.
		if len(para) > 0 && (isTableLine(line) != isTableLine(para[len(para)-1])) {
			flushBlock()
		}
		para = append(para, line)
	}
	flushSection()
	return sections
}

// isTableLine reports whether a line is part of a Markdown table.
func isTableLine(line string) bool { return strings.HasPrefix(strings.TrimSpace(line), "|") }

// newBlock classifies a run of lines as a table, a list, or prose by its first line.
func newBlock(lines []string) block {
	first := lines[0]
	switch {
	case isTableLine(first):
		return block{kind: "table", lines: lines}
	case listRE.MatchString(first):
		return block{kind: "list", lines: lines}
	default:
		return block{kind: "prose", lines: lines}
	}
}

// pieces splits oversized prose or list blocks by line. Tables are kept whole.
func pieces(blocks []block) []block {
	var out []block
	for _, b := range blocks {
		if b.kind == "table" || len(b.text()) <= maxChars {
			out = append(out, b)
			continue
		}
		var cur []string
		size := 0
		for _, l := range b.lines {
			if size > 0 && size+len(l) > maxChars {
				out = append(out, block{kind: b.kind, lines: cur})
				cur, size = nil, 0
			}
			cur = append(cur, l)
			size += len(l) + 1
		}
		if len(cur) > 0 {
			out = append(out, block{kind: b.kind, lines: cur})
		}
	}
	return out
}

// packSection packs a section's blocks into chunks under the size budget. Each chunk's content starts with the title and heading-path prefix.
func packSection(sourceID, title string, sec section, occurrence int) []Chunk {
	prefix := title
	if len(sec.path) > 0 {
		prefix = title + " > " + strings.Join(sec.path, " > ")
	}

	var out []Chunk
	var buf []block
	size := 0
	flush := func() {
		if len(buf) == 0 {
			return
		}
		out = append(out, makeChunk(sourceID, sec.path, len(out), occurrence, prefix, buf))
		buf, size = nil, 0
	}
	for _, b := range pieces(sec.blocks) {
		n := len(b.text())
		if size > 0 && size+n > maxChars {
			flush()
		}
		buf = append(buf, b)
		size += n + 2
	}
	flush()
	return out
}

// makeChunk builds one chunk from its blocks. Mixed block kinds are labelled prose.
func makeChunk(sourceID string, path []string, ordinal, occurrence int, prefix string, blocks []block) Chunk {
	kind := blocks[0].kind
	texts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if b.kind != kind {
			kind = "prose"
		}
		texts = append(texts, b.text())
	}
	return Chunk{
		ID:          stableID(sourceID, path, ordinal, occurrence),
		HeadingPath: append([]string(nil), path...),
		Content:     prefix + "\n\n" + strings.Join(texts, "\n\n"),
		Type:        kind,
	}
}

// stableID derives a chunk ID from the source, heading path, ordinal, and occurrence, so re-ingesting unchanged text gives the same ID.
func stableID(sourceID string, path []string, ordinal, occurrence int) string {
	key := fmt.Sprintf("%s\x00%s\x00%d\x00%d", sourceID, strings.Join(path, "\x1f"), ordinal, occurrence)
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}
