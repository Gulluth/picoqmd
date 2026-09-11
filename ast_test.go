package main

import (
	"strings"
	"testing"
)

func TestDetectLanguage(t *testing.T) {
	cases := map[string]SupportedLanguage{
		"a.ts":  LangTypeScript,
		"a.tsx": LangTSX,
		"a.js":  LangJavaScript,
		"a.jsx": LangTSX,
		"a.py":  LangPython,
		"a.go":  LangGo,
		"a.rs":  LangRust,
		"a.md":  "",
		"a.txt": "",
		"a":     "",
	}
	for path, want := range cases {
		if got := detectLanguage(path); got != want {
			t.Errorf("detectLanguage(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestGetASTBreakPointsGo(t *testing.T) {
	src := "package main\n\nimport \"fmt\"\n\ntype Server struct {\n\tport int\n}\n\nfunc main() {}\n"
	points := getASTBreakPoints(src, "a.go")
	if len(points) == 0 {
		t.Fatal("expected break points, got none")
	}
	// import scores 60, type scores 80, func scores 90 (qmd SCORE_MAP)
	byType := map[string]int{}
	for _, p := range points {
		byType[p.Type] = p.Score
		if !strings.HasPrefix(p.Type, "ast:") {
			t.Errorf("type %q missing ast: prefix", p.Type)
		}
	}
	if byType["ast:import"] != 60 {
		t.Errorf("ast:import score = %d, want 60", byType["ast:import"])
	}
	if byType["ast:type"] != 80 {
		t.Errorf("ast:type score = %d, want 80", byType["ast:type"])
	}
	if byType["ast:func"] != 90 {
		t.Errorf("ast:func score = %d, want 90", byType["ast:func"])
	}
}

func TestGetASTBreakPointsUnsupported(t *testing.T) {
	if pts := getASTBreakPoints("# hello\n\nworld\n", "readme.md"); len(pts) != 0 {
		t.Errorf("markdown should yield no AST points, got %v", pts)
	}
	if pts := getASTBreakPoints("x", "file.txt"); len(pts) != 0 {
		t.Errorf("unknown ext should yield no AST points, got %v", pts)
	}
}

func TestMergeBreakPoints(t *testing.T) {
	a := []BreakPoint{{Pos: 10, Score: 20, Type: "blank"}, {Pos: 50, Score: 1, Type: "newline"}}
	b := []BreakPoint{{Pos: 10, Score: 90, Type: "ast:func"}, {Pos: 75, Score: 100, Type: "ast:class"}}
	merged := mergeBreakPoints(a, b)
	if len(merged) != 3 {
		t.Fatalf("len = %d, want 3", len(merged))
	}
	for _, bp := range merged {
		if bp.Pos == 10 && bp.Score != 90 {
			t.Errorf("pos 10 score = %d, want 90 (AST wins)", bp.Score)
		}
	}
	if !(merged[0].Pos < merged[1].Pos && merged[1].Pos < merged[2].Pos) {
		t.Errorf("not sorted: %v", merged)
	}
}
