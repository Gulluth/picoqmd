// AST-aware chunking support via dcosson/treesitter-go (pure Go, no cgo).
//
// Port of qmd's src/ast.ts: language detection, per-language
// S-expression queries, score map, break-point extraction, and merging
// with regex break points. All functions degrade gracefully — parse
// failures or unsupported languages return empty slices, falling back
// to regex-only chunking.
package main

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	ts "github.com/dcosson/treesitter-go"
	iparser "github.com/dcosson/treesitter-go/parser"
	"github.com/dcosson/treesitter-go/languages/golang"
	"github.com/dcosson/treesitter-go/languages/javascript"
	"github.com/dcosson/treesitter-go/languages/python"
	"github.com/dcosson/treesitter-go/languages/rust"
	"github.com/dcosson/treesitter-go/languages/tsx"
	"github.com/dcosson/treesitter-go/languages/typescript"
)

// SupportedLanguage mirrors qmd's SupportedLanguage.
type SupportedLanguage string

const (
	LangTypeScript SupportedLanguage = "typescript"
	LangTSX        SupportedLanguage = "tsx"
	LangJavaScript SupportedLanguage = "javascript"
	LangPython     SupportedLanguage = "python"
	LangGo         SupportedLanguage = "go"
	LangRust       SupportedLanguage = "rust"
)

var extensionMap = map[string]SupportedLanguage{
	".ts":  LangTypeScript,
	".tsx": LangTSX,
	".js":  LangJavaScript,
	".jsx": LangTSX,
	".mts": LangTypeScript,
	".cts": LangTypeScript,
	".mjs": LangJavaScript,
	".cjs": LangJavaScript,
	".py":  LangPython,
	".go":  LangGo,
	".rs":  LangRust,
}

// detectLanguage returns the tree-sitter language for a file path,
// or "" for unsupported extensions (including .md).
func detectLanguage(path string) SupportedLanguage {
	ext := strings.ToLower(filepath.Ext(path))
	if lang, ok := extensionMap[ext]; ok {
		return lang
	}
	return ""
}

// languageQueries ports qmd's LANGUAGE_QUERIES verbatim.
var languageQueries = map[SupportedLanguage]string{
	LangTypeScript: `
    (export_statement) @export
    (class_declaration) @class
    (function_declaration) @func
    (method_definition) @method
    (interface_declaration) @iface
    (type_alias_declaration) @type
    (enum_declaration) @enum
    (import_statement) @import
    (lexical_declaration (variable_declarator value: (arrow_function))) @func
    (lexical_declaration (variable_declarator value: (function_expression))) @func
  `,
	LangTSX: `
    (export_statement) @export
    (class_declaration) @class
    (function_declaration) @func
    (method_definition) @method
    (interface_declaration) @iface
    (type_alias_declaration) @type
    (enum_declaration) @enum
    (import_statement) @import
    (lexical_declaration (variable_declarator value: (arrow_function))) @func
    (lexical_declaration (variable_declarator value: (function_expression))) @func
  `,
	LangJavaScript: `
    (export_statement) @export
    (class_declaration) @class
    (function_declaration) @func
    (method_definition) @method
    (import_statement) @import
    (lexical_declaration (variable_declarator value: (arrow_function))) @func
    (lexical_declaration (variable_declarator value: (function_expression))) @func
  `,
	LangPython: `
    (class_definition) @class
    (function_definition) @func
    (decorated_definition) @decorated
    (import_statement) @import
    (import_from_statement) @import
  `,
	LangGo: `
    (type_declaration) @type
    (function_declaration) @func
    (method_declaration) @method
    (import_declaration) @import
  `,
	LangRust: `
    (struct_item) @struct
    (impl_item) @impl
    (function_item) @func
    (trait_item) @trait
    (enum_item) @enum
    (use_declaration) @import
    (type_item) @type
    (mod_item) @mod
  `,
}

// scoreMap ports qmd's SCORE_MAP verbatim.
var scoreMap = map[string]int{
	"class":     100,
	"iface":     100,
	"struct":    100,
	"trait":     100,
	"impl":      100,
	"mod":       100,
	"export":    90,
	"func":      90,
	"method":    90,
	"decorated": 90,
	"type":      80,
	"enum":      80,
	"import":    60,
}

// BreakPoint is a candidate chunk boundary. Pos is a byte offset into
// the source; Score follows qmd's scale so the existing distance-decay
// in the chunker works unchanged.
type BreakPoint struct {
	Pos   int
	Score int
	Type  string
}

func languageFor(lang SupportedLanguage) *ts.Language {
	switch lang {
	case LangTypeScript:
		return typescript.Language()
	case LangTSX:
		return tsx.Language()
	case LangJavaScript:
		return javascript.Language()
	case LangPython:
		return python.Language()
	case LangGo:
		return golang.Language()
	case LangRust:
		return rust.Language()
	default:
		return nil
	}
}

// getASTBreakPoints parses content and returns break points at AST node
// boundaries. Returns an empty slice for unsupported languages or on any
// parse/query failure. Never returns an error — callers fall back to
// regex-only chunking.
func getASTBreakPoints(content, path string) []BreakPoint {
	lang := detectLanguage(path)
	if lang == "" {
		return nil
	}
	tsLang := languageFor(lang)
	if tsLang == nil {
		return nil
	}
	qsrc, ok := languageQueries[lang]
	if !ok {
		return nil
	}

	ctx := context.Background()
	p := iparser.NewParser()
	p.SetLanguage(tsLang)
	tree := p.ParseString(ctx, []byte(content))
	if tree == nil {
		return nil
	}
	root := tree.RootNode()

	q, err := ts.NewQuery(tsLang, qsrc)
	if err != nil {
		return nil
	}
	cursor := ts.NewQueryCursor(q)
	cursor.Exec(root)

	seen := make(map[int]BreakPoint)
	for {
		m, ok := cursor.NextMatch()
		if !ok {
			break
		}
		for _, c := range m.Captures {
			name := q.CaptureNameForID(c.Index)
			score, ok := scoreMap[name]
			if !ok {
				score = 20
			}
			pos := int(c.Node.StartByte())
			if existing, dup := seen[pos]; !dup || score > existing.Score {
				seen[pos] = BreakPoint{Pos: pos, Score: score, Type: "ast:" + name}
			}
		}
	}

	out := make([]BreakPoint, 0, len(seen))
	for _, bp := range seen {
		out = append(out, bp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pos < out[j].Pos })
	return out
}

// mergeBreakPoints combines two break-point sets, keeping the highest
// score at each position. Result is sorted by position.
func mergeBreakPoints(a, b []BreakPoint) []BreakPoint {
	seen := make(map[int]BreakPoint, len(a)+len(b))
	for _, bp := range a {
		if existing, ok := seen[bp.Pos]; !ok || bp.Score > existing.Score {
			seen[bp.Pos] = bp
		}
	}
	for _, bp := range b {
		if existing, ok := seen[bp.Pos]; !ok || bp.Score > existing.Score {
			seen[bp.Pos] = bp
		}
	}
	out := make([]BreakPoint, 0, len(seen))
	for _, bp := range seen {
		out = append(out, bp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pos < out[j].Pos })
	return out
}
