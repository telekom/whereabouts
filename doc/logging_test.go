// Copyright 2026 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package doc

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the generated log tables in logging.md")

const module = "github.com/telekom/whereabouts/"

// components maps each generated table in logging.md to its binary. The
// test scans exactly the module packages the binary links (go list -deps),
// so a package that starts or stops being a dependency moves in or out of
// the table on its own.
var components = []struct{ name, pkg string }{
	{"cni-plugin", "./cmd/whereabouts"},
	{"operator", "./cmd/operator"},
	{"install-cni", "./cmd/install-cni"},
}

// TestLoggingReference fails when a log call in the code is missing from
// doc/logging.md, the document lists one the code no longer makes, a log
// call has no literal message, or code writes output that bypasses the
// documented loggers. Regenerate the tables with
// "go test ./doc -run TestLoggingReference -update".
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
		want := renderTable(scanBinary(t, c.pkg))
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

// scanBinary returns the log calls in the non-test Go files of every module
// package the binary at pkg links.
func scanBinary(t *testing.T, pkg string) []logCall {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "-f", `{{.ImportPath}}|{{.Dir}}|{{join .GoFiles ","}}`, pkg) //nolint:gosec // pkg comes from the fixed components table
	cmd.Dir = ".."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", pkg, err)
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	var calls []logCall
	fset := token.NewFileSet()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 || !strings.HasPrefix(parts[0], module) || parts[2] == "" {
			continue
		}
		for _, name := range strings.Split(parts[2], ",") {
			path := filepath.Join(parts[1], name)
			f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatal(err)
			}
			v := visitor{imports: imports(f), source: filepath.ToSlash(rel), loggerImpl: parts[0] == module+"pkg/logging"}
			ast.Inspect(f, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					c, isLog, problem := v.classify(call)
					if problem != "" {
						t.Errorf("%s: %s", fset.Position(call.Pos()), problem)
					} else if isLog {
						calls = append(calls, c)
					}
				}
				return true
			})
		}
	}
	return calls
}

// imports maps each file-level package name (alias or default) to its path.
func imports(f *ast.File) map[string]string {
	m := map[string]string{}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		name := p[strings.LastIndex(p, "/")+1:]
		if strings.HasPrefix(name, "v") && strings.Contains(p, "/") { // k8s.io/klog/v2
			if _, err := strconv.Atoi(name[1:]); err == nil {
				q := strings.TrimSuffix(p, "/"+name)
				name = q[strings.LastIndex(q, "/")+1:]
			}
		}
		if imp.Name != nil {
			name = imp.Name.Name
		}
		m[name] = p
	}
	return m
}

type visitor struct {
	imports map[string]string
	source  string
	// loggerImpl marks pkg/logging itself, whose stderr writes of the
	// formatted line are the CNI plugin's log output, not separate messages.
	loggerImpl bool
}

// pkgOf returns the import path when e is a package identifier.
func (v visitor) pkgOf(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return v.imports[id.Name]
	}
	return ""
}

// classify recognizes the log calls of the three loggers in this repository:
// pkg/logging (CNI plugin), log/slog (install-cni), logr (operator), plus
// direct writes to stderr. problem is set for a log call without a literal
// message and for output that bypasses these loggers.
func (v visitor) classify(call *ast.CallExpr) (c logCall, isLog bool, problem string) {
	if id, ok := call.Fun.(*ast.Ident); ok && (id.Name == "print" || id.Name == "println") {
		return c, false, "builtin " + id.Name + " bypasses the documented loggers"
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return c, false, ""
	}
	name, pkg := sel.Sel.Name, v.pkgOf(sel.X)
	switch pkg {
	case module + "pkg/logging":
		switch name {
		case "Debugf", "Verbosef", "Errorf":
			return v.printfCall(strings.ToLower(strings.TrimSuffix(name, "f")), call.Args, 0)
		case "Printf":
			lvl := strings.TrimSuffix(types.ExprString(call.Args[0]), "Level")
			lvl = lvl[strings.LastIndex(lvl, ".")+1:]
			return v.printfCall(strings.ToLower(lvl), call.Args, 1)
		}
		return c, false, ""
	case "log/slog":
		switch name {
		case "Debug", "Info", "Warn", "Error":
			return v.kvCall(strings.ToLower(name), call.Args, 0)
		case "DebugContext", "InfoContext", "WarnContext", "ErrorContext":
			return v.kvCall(strings.ToLower(strings.TrimSuffix(name, "Context")), call.Args, 1)
		case "Log", "LogAttrs":
			return v.kvCall(slogLevel(call.Args[1]), call.Args, 2)
		}
		return c, false, ""
	case "fmt":
		if strings.HasPrefix(name, "Print") {
			return c, false, "fmt." + name + " writes to stdout, bypassing the documented loggers"
		}
		if strings.HasPrefix(name, "Fprint") && len(call.Args) > 1 {
			switch types.ExprString(call.Args[0]) {
			case "os.Stderr":
				return v.printfCall("stderr", call.Args, 1)
			case "os.Stdout":
				return c, false, "fmt." + name + "(os.Stdout) bypasses the documented loggers"
			}
		}
		return c, false, ""
	case "log", "k8s.io/klog/v2", "go.uber.org/zap":
		return c, false, pkg + "." + name + " bypasses the documented loggers"
	case "":
	default:
		return c, false, ""
	}

	recv := types.ExprString(sel.X)
	switch {
	case recv == "os.Stderr" && strings.HasPrefix(name, "Write"):
		return v.printfCall("stderr", call.Args, 0)
	case recv == "os.Stdout" && strings.HasPrefix(name, "Write"):
		return c, false, "os.Stdout." + name + " bypasses the documented loggers"
	case name == "Warn" || name == "Debug": // *slog.Logger; logr has neither
		return v.kvCall(strings.ToLower(name), call.Args, 0)
	case strings.HasSuffix(name, "Context") && slices.Contains([]string{"DebugContext", "InfoContext", "WarnContext", "ErrorContext"}, name):
		return v.kvCall(strings.ToLower(strings.TrimSuffix(name, "Context")), call.Args, 1)
	case name == "Info": // logr or *slog.Logger
		return v.kvCall(logrLevel(sel.X), call.Args, 0)
	case name == "Error" && len(call.Args) > 0:
		if _, ok := message(call.Args[0]); ok { // *slog.Logger: Error(msg, kv...)
			return v.kvCall("error", call.Args, 0)
		}
		if len(call.Args) > 1 { // logr: Error(err, msg, kv...)
			c, isLog, problem = v.kvCall("error", call.Args, 1)
			c.fields = strings.TrimSuffix("`error`, "+c.fields, ", ")
			return c, isLog, problem
		}
	}
	return c, false, ""
}

// logrLevel walks x.V(n).WithValues(...).WithName(...) receivers for V(n).
func logrLevel(e ast.Expr) string {
	for {
		call, ok := e.(*ast.CallExpr)
		if !ok {
			return "info"
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return "info"
		}
		if sel.Sel.Name == "V" && len(call.Args) == 1 {
			if level := "V(" + types.ExprString(call.Args[0]) + ")"; level != "V(1)" {
				return level
			}
			return "debug"
		}
		e = sel.X
	}
}

func slogLevel(e ast.Expr) string {
	s := types.ExprString(e)
	return strings.ToLower(strings.TrimPrefix(s[strings.LastIndex(s, ".")+1:], "Level"))
}

func (v visitor) printfCall(level string, args []ast.Expr, msgIdx int) (logCall, bool, string) {
	if len(args) <= msgIdx {
		return logCall{}, false, ""
	}
	msg, ok := message(args[msgIdx])
	if !ok {
		if v.loggerImpl && level == "stderr" {
			return logCall{}, false, ""
		}
		return logCall{}, false, fmt.Sprintf("%s log call needs a literal message, got %s", level, types.ExprString(args[msgIdx]))
	}
	var fields []string
	for _, a := range args[msgIdx+1:] {
		fields = append(fields, "`"+types.ExprString(a)+"`")
	}
	return logCall{level: level, msg: msg, fields: strings.Join(fields, ", "), source: v.source}, true, ""
}

func (v visitor) kvCall(level string, args []ast.Expr, msgIdx int) (logCall, bool, string) {
	if len(args) <= msgIdx {
		return logCall{}, false, ""
	}
	msg, ok := message(args[msgIdx])
	if !ok {
		return logCall{}, false, fmt.Sprintf("%s log call needs a literal message, got %s", level, types.ExprString(args[msgIdx]))
	}
	var fields []string
	for i := msgIdx + 1; i < len(args); i += 2 {
		if lit, ok := args[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			key, _ := strconv.Unquote(lit.Value)
			fields = append(fields, "`"+key+"`")
		} else {
			fields = append(fields, "`<"+types.ExprString(args[i])+">`")
		}
	}
	return logCall{level: level, msg: msg, fields: strings.Join(fields, ", "), source: v.source}, true, ""
}

// message renders a string literal or a concatenation containing one; other
// operands appear as <expr>. Anything else is not a literal message.
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

// TestLoggingCheckClassify pins what the drift check records and rejects.
func TestLoggingCheckClassify(t *testing.T) {
	const src = `package x

import (
	"fmt"
	stdlog "log"
	"os"
	sl "log/slog"

	wlog "github.com/telekom/whereabouts/pkg/logging"
	"k8s.io/klog/v2"
)

func f(l logger, s *sl.Logger, msg string, err error) {
	wlog.Debugf("lit %s", msg)
	wlog.Errorf(msg)
	sl.InfoContext(ctx, "ctx info", "k", 1)
	sl.Log(ctx, sl.LevelWarn, "logged", "k", 1)
	s.Warn("logger warn", "k", 1)
	s.Error("logger error")
	l.V(1).WithValues("a", 1).Info("deep debug", "b", 2)
	l.V(2).Info("trace")
	l.Error(err, "failed", "k", 1)
	l.Info(msg)
	fmt.Fprintf(os.Stderr, "to stderr %v\n", err)
	stdlog.Printf("x")
	klog.Info("x")
	fmt.Println("x")
	println("x")
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	v := visitor{imports: imports(f), source: "x.go"}
	var got []string
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			c, isLog, problem := v.classify(call)
			switch {
			case problem != "":
				got = append(got, "PROBLEM "+types.ExprString(call.Fun))
			case isLog:
				got = append(got, c.level+" "+c.msg+" "+c.fields)
			}
		}
		return true
	})
	want := []string{
		"debug lit %s `msg`",
		"PROBLEM wlog.Errorf",
		"info ctx info `k`",
		"warn logged `k`",
		"warn logger warn `k`",
		"error logger error ",
		"debug deep debug `b`",
		"V(2) trace ",
		"error failed `error`, `k`",
		"PROBLEM l.Info",
		"stderr to stderr %v `err`",
		"PROBLEM stdlog.Printf",
		"PROBLEM klog.Info",
		"PROBLEM fmt.Println",
		"PROBLEM println",
	}
	if !slices.Equal(got, want) {
		t.Errorf("classify:\n got %q\nwant %q", got, want)
	}
}
