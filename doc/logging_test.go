// Copyright 2026 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package doc

import (
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the generated log tables in logging.md")

// components maps each generated table in logging.md to the source
// directories (relative to the repository root) of the binary it documents.
var components = []struct {
	name string
	dirs []string
}{
	{"cni-plugin", []string{"cmd/whereabouts", "pkg"}},
	{"operator", []string{"cmd/operator", "internal"}},
	{"install-cni", []string{"cmd/install-cni"}},
}

// TestLoggingReference fails when a log call in the code is missing from
// doc/logging.md, or the document lists one the code no longer makes.
// Regenerate the tables with: go test ./doc -run TestLoggingReference -update
func TestLoggingReference(t *testing.T) {
	const docPath = "logging.md"
	raw, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, c := range components {
		begin := "<!-- BEGIN GENERATED LOG TABLE: " + c.name + " -->\n"
		end := "<!-- END GENERATED LOG TABLE: " + c.name + " -->"
		i, j := strings.Index(doc, begin), strings.Index(doc, end)
		if i < 0 || j < i {
			t.Fatalf("%s: markers for %q missing", docPath, c.name)
		}
		var calls []logCall
		for _, d := range c.dirs {
			calls = append(calls, scanDir(t, filepath.Join("..", d))...)
		}
		want := renderTable(calls)
		if got := doc[i+len(begin) : j]; got != want {
			if !*update {
				t.Errorf("%s: log table %q is out of date with the code; run: go test ./doc -run TestLoggingReference -update\n--- want\n%s", docPath, c.name, want)
				continue
			}
			doc = doc[:i+len(begin)] + want + doc[j:]
		}
	}
	if *update {
		if err := os.WriteFile(docPath, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

type logCall struct {
	level, msg, fields, source string
}

func scanDir(t *testing.T, dir string) []logCall {
	t.Helper()
	var calls []logCall
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "generated" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		source := filepath.ToSlash(strings.TrimPrefix(path, ".."+string(filepath.Separator)))
		ast.Inspect(f, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if c, ok := classify(call); ok {
					c.source = source
					calls = append(calls, c)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return calls
}

// classify recognises the log calls of the three loggers in this repository:
// pkg/logging (CNI plugin), log/slog (install-cni), logr (operator), plus
// direct writes to stderr.
func classify(call *ast.CallExpr) (logCall, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return logCall{}, false
	}
	name, recv := sel.Sel.Name, types.ExprString(sel.X)
	switch {
	case recv == "logging":
		switch name {
		case "Debugf", "Verbosef", "Errorf":
			return printfCall(strings.ToLower(strings.TrimSuffix(name, "f")), call.Args, 0)
		case "Printf":
			if len(call.Args) > 0 {
				lvl := strings.TrimSuffix(strings.TrimPrefix(types.ExprString(call.Args[0]), "logging."), "Level")
				return printfCall(strings.ToLower(lvl), call.Args, 1)
			}
		}
	case recv == "slog":
		switch name {
		case "Debug", "Info", "Warn", "Error":
			return kvCall(strings.ToLower(name), call.Args, 0)
		}
	case recv == "fmt" && strings.HasPrefix(name, "Fprint"):
		if len(call.Args) > 1 && types.ExprString(call.Args[0]) == "os.Stderr" {
			return printfCall("stderr", call.Args, 1)
		}
	case recv == "os.Stderr" && name == "WriteString":
		return printfCall("stderr", call.Args, 0)
	case name == "Info": // logr
		level := "info"
		if v, ok := sel.X.(*ast.CallExpr); ok && len(v.Args) == 1 {
			if vs, ok := v.Fun.(*ast.SelectorExpr); ok && vs.Sel.Name == "V" {
				level = "V(" + types.ExprString(v.Args[0]) + ")"
				if level == "V(1)" {
					level = "debug"
				}
			}
		}
		return kvCall(level, call.Args, 0)
	case name == "Error": // logr
		return kvCall("error", call.Args, 1)
	}
	return logCall{}, false
}

func printfCall(level string, args []ast.Expr, msgIdx int) (logCall, bool) {
	if len(args) <= msgIdx {
		return logCall{}, false
	}
	msg, ok := message(args[msgIdx])
	if !ok {
		return logCall{}, false
	}
	var fields []string
	for _, a := range args[msgIdx+1:] {
		fields = append(fields, "`"+types.ExprString(a)+"`")
	}
	return logCall{level: level, msg: msg, fields: strings.Join(fields, ", ")}, true
}

func kvCall(level string, args []ast.Expr, msgIdx int) (logCall, bool) {
	if len(args) <= msgIdx {
		return logCall{}, false
	}
	msg, ok := message(args[msgIdx])
	if !ok {
		return logCall{}, false
	}
	var fields []string
	if msgIdx == 1 {
		fields = append(fields, "`error`")
	}
	for i := msgIdx + 1; i < len(args); i += 2 {
		if lit, ok := args[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			key, _ := strconv.Unquote(lit.Value)
			fields = append(fields, "`"+key+"`")
		} else {
			fields = append(fields, "`<"+types.ExprString(args[i])+">`")
		}
	}
	return logCall{level: level, msg: msg, fields: strings.Join(fields, ", ")}, true
}

// message renders a string literal or a concatenation containing one; other
// operands appear as <expr>. Anything else is not a log message.
func message(e ast.Expr) (string, bool) {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(e.Value)
		return strings.TrimSuffix(s, "\n"), err == nil
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		l, lok := message(e.X)
		r, rok := message(e.Y)
		if !lok && !rok {
			return "", false
		}
		if !lok {
			l = "<" + types.ExprString(e.X) + ">"
		}
		if !rok {
			r = "<" + types.ExprString(e.Y) + ">"
		}
		return l + r, true
	}
	return "", false
}

var levelOrder = []string{"stderr", "panic", "error", "warn", "info", "verbose", "debug"}

func levelRank(l string) int {
	if i := slices.Index(levelOrder, l); i >= 0 {
		return i
	}
	return len(levelOrder) // V(n) with n > 1
}

// renderTable groups identical calls and lists them by level, then message.
func renderTable(calls []logCall) string {
	type key struct{ level, msg, fields string }
	sources := map[key][]string{}
	var keys []key
	for _, c := range calls {
		k := key{c.level, c.msg, c.fields}
		if _, ok := sources[k]; !ok {
			keys = append(keys, k)
		}
		if !slices.Contains(sources[k], c.source) {
			sources[k] = append(sources[k], c.source)
		}
	}
	slices.SortFunc(keys, func(a, b key) int {
		if d := levelRank(a.level) - levelRank(b.level); d != 0 {
			return d
		}
		if c := strings.Compare(a.level, b.level); c != 0 {
			return c
		}
		if c := strings.Compare(a.msg, b.msg); c != 0 {
			return c
		}
		return strings.Compare(a.fields, b.fields)
	})
	var b strings.Builder
	b.WriteString("| Level | Message | Values / fields | Source |\n|---|---|---|---|\n")
	esc := strings.NewReplacer("|", `\|`, "\n", " ").Replace
	for _, k := range keys {
		src := sources[k]
		slices.Sort(src)
		fields := k.fields
		if fields == "" {
			fields = "-"
		}
		b.WriteString("| " + k.level + " | `` " + esc(k.msg) + " `` | " + esc(fields) + " | " + strings.Join(src, ", ") + " |\n")
	}
	return b.String()
}
