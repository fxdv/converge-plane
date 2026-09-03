// Package specsync keeps the product specification and the codebase in
// lockstep.
//
// The product spec (docs/spec/product-spec.tex) tags every feature with a
// token: \cs{domain:token} for the feature's canonical anchor (exactly
// one per token) and \csr{domain:token} for reference-only mentions.
// Code references the same token in a comment: the literal prefix
// "spec cs:" followed by the token (Go, TypeScript, SQL — any scanned
// file).
//
// The check is bidirectional:
//   - a code reference to a token that does not exist in the spec is a
//     dangling anchor: FAIL (someone references a feature the spec
//     does not know about).
//   - a duplicate canonical anchor (\cs used twice for one token) is a
//     LaTeX \label error: FAIL.
//   - spec tags no code references yet are printed as a reminder (not a
//     failure): they mark spec-only surface (product narrative, roadmap,
//     moat) or features whose code is not tagged yet.
//
// The same permanent-guarantee philosophy as the wire-contract tests:
// documentation cannot rot silently, because the drift fails the gate.
package specsync

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SpecPath is the product spec, relative to the repository root.
const SpecPath = "docs/spec/product-spec.tex"

// TokenPattern is the token vocabulary: lowercase segments (letters,
// digits, and hyphens; each starting with a letter) joined by colons,
// at least domain:token (cs:swarm:handoff in code, swarm:handoff in
// the spec macro argument).
var TokenPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*(?::[a-z][a-z0-9-]*)+$`)

// tokenRe matches spec macro arguments (\cs{...} / \csr{...}).
var tokenRe = regexp.MustCompile(`\\cs(?:r)?\{([^}]+)\}`)

// codeRefRe matches code-side references — the "spec cs:" prefix
// followed by the token — in any scanned file's text.
var codeRefRe = regexp.MustCompile(`spec cs:([a-z][a-z0-9-]*(?::[a-z][a-z0-9-]*)+)`)

// dirs to never walk: dependency and build output trees.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, ".next": true, "dist": true,
	"out": true, "coverage": true,
}

// fileExts are scanned for code references.
var fileExts = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".sql": true, ".sh": true,
}

// Spec is the parsed product-spec tag inventory.
type Spec struct {
	// Anchors are the canonical \cs{...} occurrences.
	Anchors map[string][]string // token -> "chapter-line" hints
	// Mentions are the reference-only \csr{...} occurrences.
	Mentions map[string]int
}

// Check runs the specsync validation against the repository at root.
// It returns (report, error): error is non-nil on a gate failure;
// report is the human-readable summary, always safe to print.
func Check(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, SpecPath))
	if err != nil {
		return "", fmt.Errorf("specsync: cannot read spec: %w", err)
	}
	spec := parseSpec(string(data))

	codeRefs, err := collectCodeRefs(root)
	if err != nil {
		return "", fmt.Errorf("specsync: %w", err)
	}

	var failures []string

	// 1. Dangling code references: the code points at a token the spec
	//    does not define.
	allTokens := make(map[string]bool, len(spec.Anchors)+len(spec.Mentions))
	for t := range spec.Anchors {
		allTokens[t] = true
	}
	for t := range spec.Mentions {
		allTokens[t] = true
	}
	for ref := range codeRefs {
		if !allTokens[ref] {
			files := codeRefs[ref]
			failures = append(failures,
				fmt.Sprintf("dangling code reference cs:%s (spec defines no such token): %s", ref, strings.Join(files, ", ")))
		}
	}

	// 2. Duplicate canonical anchors: the spec defines one token twice
	//    with \cs (the reference-only \csr is the form for mentions).
	for t, where := range spec.Anchors {
		if len(where) > 1 {
			failures = append(failures,
				fmt.Sprintf("duplicate canonical anchor \\cs{%s}: %s (use \\csr for mentions)", t, strings.Join(where, ", ")))
		}
	}

	// 3. Malformed tokens, in the spec or in code.
	for t := range allTokens {
		if !TokenPattern.MatchString(t) {
			failures = append(failures, fmt.Sprintf("malformed spec token %q (want domain:token, lowercase)", t))
		}
	}
	for ref := range codeRefs {
		if !TokenPattern.MatchString(ref) {
			failures = append(failures, fmt.Sprintf("malformed code reference cs:%s (want domain:token, lowercase)", ref))
		}
	}

	sort.Strings(failures)
	if len(failures) > 0 {
		return render(spec, codeRefs), fmt.Errorf("specsync: %d failure(s):\n  %s", len(failures), strings.Join(failures, "\n  "))
	}
	return render(spec, codeRefs), nil
}

// render produces the report: coverage of the spec by code references,
// with the untagged remainder listed as a reminder.
func render(spec *Spec, codeRefs map[string][]string) string {
	var tagged, untagged []string
	known := make(map[string]bool)
	for t := range spec.Anchors {
		known[t] = true
	}
	for t := range spec.Mentions {
		known[t] = true
	}
	for t := range known {
		if len(codeRefs[t]) > 0 {
			tagged = append(tagged, t)
		} else {
			untagged = append(untagged, t)
		}
	}
	sort.Strings(tagged)
	sort.Strings(untagged)

	var b strings.Builder
	fmt.Fprintf(&b, "specsync: %d spec tags, %d referenced from code, %d spec-only\n", len(known), len(tagged), len(untagged))
	if len(untagged) > 0 {
		b.WriteString("spec tags not (yet) referenced from code: " + strings.Join(untagged, ", ") + "\n")
		b.WriteString("(spec-only surface such as product narrative, roadmap, and moat is expected here; add the tag to code as features land)\n")
	}
	return b.String()
}

// parseSpec extracts \cs{...} anchors and \csr{...} mentions. LaTeX
// comments are blanked first: a macro that sits in a % comment is an
// example in the document's prose, not a tag.
func parseSpec(src string) *Spec {
	s := &Spec{Anchors: map[string][]string{}, Mentions: map[string]int{}}
	c := blankComments(src)
	for _, m := range tokenRe.FindAllStringSubmatchIndex(c, -1) {
		tok := c[m[2]:m[3]]
		// The text from the match start to the capture start is the
		// macro prefix including its opening brace: "\cs{" or "\csr{".
		switch which := strings.TrimSuffix(c[m[0]:m[2]], "{"); which {
		case "\\csr":
			s.Mentions[tok]++
		default: // \cs — canonical anchor
			s.Anchors[tok] = append(s.Anchors[tok], positionOf(c, m[0]))
		}
	}
	return s
}

// blankComments replaces LaTeX comment runs (% to end of line) with
// spaces, preserving length and line offsets. A percent preceded by an
// odd number of backslashes is an escape (\%), not a comment.
func blankComments(src string) string {
	b := []byte(src)
	for i := 0; i < len(b); i++ {
		if b[i] != '%' {
			continue
		}
		back := 0
		for j := i - 1; j >= 0 && b[j] == '\\'; j-- {
			back++
		}
		if back%2 != 0 {
			continue // escaped percent, part of the content
		}
		for j := i; j < len(b) && b[j] != '\n'; j++ {
			b[j] = ' '
		}
	}
	return string(b)
}

// positionOf renders a source offset as "line N" for readable failures.
func positionOf(src string, offset int) string {
	line := 1
	for i := 0; i < offset && i < len(src); i++ {
		if src[i] == '\n' {
			line++
		}
	}
	return fmt.Sprintf("line %d", line)
}

// collectCodeRefs walks the repository (pruning dependency and build
// trees) and returns every spec token referenced from code, with the
// files that reference it.
func collectCodeRefs(root string) (map[string][]string, error) {
	refs := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !fileExts[filepath.Ext(d.Name())] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, m := range codeRefRe.FindAllStringSubmatch(string(data), -1) {
			tok := m[1]
			refs[tok] = append(refs[tok], rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for t := range refs {
		sort.Strings(refs[t])
	}
	return refs, nil
}
