// Package frontmatter parses and renders the ---fenced YAML frontmatter
// blocks that prefix skill, command, and memory files.
package frontmatter

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Split separates an optional leading ---fenced block from the body. It returns
// the parsed keys (lowercased) and the remaining body. With no opening/closing
// fence the whole input is the body. An opened but never closed fence treats the
// entire input as body (no partial parse).
//
// Scalar values are converted to strings. Mapping values are flattened one level
// so "metadata:\n  type: user" becomes fm["type"] = "user", matching the legacy
// parser. Sequence values are joined comma-separated (allowed-tools →
// "read_file, grep"), so list-valued keys from skills authored for other agent
// tools survive. The last write wins for duplicate keys.
func Split(s string) (map[string]string, string) {
	fm := map[string]string{}
	raw, body, ok := Raw(s)
	if !ok {
		return fm, body
	}
	parseYAMLFrontmatter(raw, fm)
	return fm, body
}

// Raw separates an optional leading ---fenced block, returning the raw YAML
// text and the body. Callers that need key detection beyond Split's string
// flattening (for example, marker keys whose values are mappings) scan the
// raw block instead of re-implementing the fence scan.
func Raw(s string) (raw, body string, ok bool) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", s, false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n"), true
		}
	}
	return "", s, false // opened but never closed: treat all as body
}

// ParseError reports whether s carries a ---fenced block that is not valid
// YAML. No fence, an empty block, or a valid block all yield nil. Use it when
// Split's permissive map (which silently yields nothing on bad YAML) is not
// enough and the author should hear about the typo.
func ParseError(s string) error {
	raw, _, ok := Raw(s)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return err
	}
	return nil
}

// Encode renders frontmatter (a struct or map marshaled as YAML at indent 2)
// followed by the body in one ---fenced document, the single writer format for
// memory files, skill files, and skill stubs. The body is right-trimmed and
// ends with exactly one newline; an empty body renders as a lone trailing
// newline after the fence.
func Encode(frontmatter any, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	// Callers pass flat structs and maps of scalars; encoding cannot fail.
	_ = enc.Encode(frontmatter)
	_ = enc.Close()
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimRight(body, " \t\r\n"))
	b.WriteString("\n")
	return b.String()
}

func parseYAMLFrontmatter(content string, out map[string]string) {
	if strings.TrimSpace(content) == "" {
		return
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return
	}
	root := mappingRoot(&doc)
	if root == nil {
		return
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := normalizeKey(root.Content[i].Value)
		if key == "" {
			continue
		}
		addYAMLValue(out, key, root.Content[i+1])
	}
}

func mappingRoot(doc *yaml.Node) *yaml.Node {
	if doc == nil {
		return nil
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return nil
	}
	return doc
}

func addYAMLValue(out map[string]string, key string, value *yaml.Node) {
	switch {
	case value == nil:
		return
	case value.Kind == yaml.MappingNode:
		for i := 0; i+1 < len(value.Content); i += 2 {
			nestedKey := normalizeKey(value.Content[i].Value)
			if nestedKey == "" {
				continue
			}
			addYAMLValue(out, nestedKey, value.Content[i+1])
		}
	case value.Kind == yaml.SequenceNode:
		items := make([]string, 0, len(value.Content))
		for _, item := range value.Content {
			if s := yamlScalarString(item); s != "" {
				items = append(items, s)
			}
		}
		if len(items) > 0 {
			joined := strings.Join(items, ", ")
			if key == "argument-hint" {
				joined = "[" + joined + "]"
			}
			out[key] = joined
		}
	default:
		if s := yamlScalarString(value); s != "" {
			out[key] = s
		}
	}
}

func normalizeKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

func yamlScalarString(node *yaml.Node) string {
	if node == nil {
		return ""
	}
	if node.Kind != yaml.ScalarNode {
		return strings.TrimSpace(fmt.Sprint(node.Value))
	}
	return strings.TrimSpace(node.Value)
}
