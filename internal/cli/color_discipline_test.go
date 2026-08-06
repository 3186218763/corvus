package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// colorCSI matches anchored color-only CSI SGR sequences (ends with 'm').
// Deliberately does NOT match \033[K (erase), \033[3m (italic), \033[1m (bold),
// \033[7m (reverse), or OSC sequences.
var colorCSI = regexp.MustCompile(
	`\x1b\[[34][0-7]m` +
		`|\x1b\[9[0-7]m` +
		`|\x1b\[10[0-7]m` +
		`|\x1b\[38;5;[0-9]+m` +
		`|\x1b\[48;5;[0-9]+m` +
		`|\x1b\[38;2;[0-9]+;[0-9]+;[0-9]+m` +
		`|\x1b\[48;2;[0-9]+;[0-9]+;[0-9]+m`,
)

func TestNoHardcodedColorCodes(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") ||
			name == "theme.go" || name == "style.go" {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if m := colorCSI.FindString(s); m != "" {
				t.Errorf("%s: hardcoded color sequence %q in string literal", fset.Position(lit.Pos()), m)
			}
			return true
		})
	}
}
