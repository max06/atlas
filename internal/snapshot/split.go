package snapshot

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Split rewrites every *.yaml file below root into one file per resource,
// named <kind>-<name>.yaml (lower case, ".", "/" and ":" replaced by "-"),
// in the directory of the source file.
//
// Why: helm's kustomize post-renderer (patches) writes all resources of a
// chart into one patched_resources.yaml, and CRD bundles hold many documents.
// Pairing files by resource instead of by source file keeps the per-resource
// diff stable when a PR adds or removes a patch. Single-document files are
// renamed the same way.
//
// The document text is kept as rendered (comments included). A document with
// no content (only comments) is prepended to the next one, as yq's split did.
// Two resources with the same file name in one directory (same kind and
// name, e.g. in two namespaces) get "-2", "-3" suffixes in document order
// (yq's split let the last one win and dropped the others from the diff).
// A file that does not parse stays as it is.
func Split(root string) error {
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil
	}
	byDir := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && strings.HasSuffix(d.Name(), ".yaml") {
			dir := filepath.Dir(path)
			byDir[dir] = append(byDir[dir], path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		if err := splitDir(dir, byDir[dir]); err != nil {
			return err
		}
	}
	return nil
}

type splitDoc struct {
	name string
	text []byte
}

func splitDir(dir string, files []string) error {
	sort.Strings(files)
	type parsed struct {
		file string
		docs []splitDoc
	}
	var ok []parsed
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		docs, err := documents(data)
		if err != nil {
			continue // unparsable: leave the file alone
		}
		ok = append(ok, parsed{f, docs})
	}
	used := map[string]bool{}
	// names of files that stay untouched (unparsable) are taken
	for _, f := range files {
		used[filepath.Base(f)] = true
	}
	for _, p := range ok {
		delete(used, filepath.Base(p.file))
	}
	var writes []splitDoc
	for _, p := range ok {
		for _, d := range p.docs {
			name := d.name + ".yaml"
			for i := 2; used[name]; i++ {
				name = fmt.Sprintf("%s-%d.yaml", d.name, i)
			}
			used[name] = true
			writes = append(writes, splitDoc{name, d.text})
		}
	}
	for _, p := range ok {
		if err := os.Remove(p.file); err != nil {
			return err
		}
	}
	for _, w := range writes {
		if err := os.WriteFile(filepath.Join(dir, w.name), w.text, 0o644); err != nil {
			return err
		}
	}
	return nil
}

var nameReplacer = regexp.MustCompile(`[./:]`)

// FileName is the per-resource file name stem: "<kind>-<name>", lower case,
// ".", "/" and ":" replaced by "-" (the yq expression diff.sh used).
func FileName(kind, name string) string {
	return nameReplacer.ReplaceAllString(strings.ToLower(kind+"-"+name), "-")
}

// documents splits a YAML stream into its resources. Comment-only documents
// are carried into the next resource; trailing ones are dropped.
func documents(data []byte) ([]splitDoc, error) {
	var out []splitDoc
	var pending []byte
	for _, raw := range splitStream(data) {
		var node yaml.Node
		if err := yaml.Unmarshal(raw, &node); err != nil {
			return nil, err
		}
		if len(node.Content) == 0 || isNull(node.Content[0]) {
			if len(bytes.TrimSpace(raw)) > 0 {
				pending = append(pending, raw...)
			}
			continue
		}
		kind, name := KindName(node.Content[0])
		text := append(append([]byte{}, pending...), raw...)
		pending = nil
		out = append(out, splitDoc{name: FileName(kind, name), text: text})
	}
	return out, nil
}

func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

// splitStream cuts a YAML stream at document markers ("---" at column 0).
// Each part ends with a newline; the markers themselves are dropped.
func splitStream(data []byte) [][]byte {
	var parts [][]byte
	var cur []byte
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		trimmed := bytes.TrimRight(line, "\r\n")
		if bytes.Equal(trimmed, []byte("---")) || bytes.HasPrefix(trimmed, []byte("--- ")) {
			parts = append(parts, cur)
			cur = nil
			if rest := bytes.TrimSpace(trimmed[3:]); len(rest) > 0 {
				// "--- <content>": the content starts the document (copied —
				// rest shares the input's memory)
				cur = append(append([]byte{}, rest...), '\n')
			}
			continue
		}
		if bytes.Equal(trimmed, []byte("...")) {
			continue
		}
		cur = append(cur, line...)
	}
	parts = append(parts, cur)
	var out [][]byte
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		if p[len(p)-1] != '\n' {
			p = append(p, '\n')
		}
		out = append(out, p)
	}
	return out
}

// KindName reads kind and metadata.name of a resource document.
func KindName(doc *yaml.Node) (kind, name string) {
	if doc.Kind != yaml.MappingNode {
		return "", ""
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		switch doc.Content[i].Value {
		case "kind":
			kind = doc.Content[i+1].Value
		case "metadata":
			md := doc.Content[i+1]
			for j := 0; md.Kind == yaml.MappingNode && j+1 < len(md.Content); j += 2 {
				if md.Content[j].Value == "name" {
					name = md.Content[j+1].Value
				}
			}
		}
	}
	return kind, name
}
