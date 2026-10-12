package snapshot

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ScrubStatus is the replay result of one baseline release.
type ScrubStatus struct {
	Status  string // scrubbed, no-map, no-yaml
	Release string // <cluster…>/<deployment>/<release>
}

func (s ScrubStatus) String() string { return s.Status + " " + s.Release }

// MarkerPrefix starts every structural Secret marker: REDACTED:sha256:<12 hex>.
const MarkerPrefix = "REDACTED:sha256:"

// Marker is the structural redaction of one Secret value: the first 12 hex
// digits of the sha256 of the value as written (for data: the base64 form).
// Deterministic and unsalted, so the same value yields the same marker on
// both sides of a review, and a rotated value shows as a changed marker.
func Marker(value string) string {
	sum := sha256.Sum256([]byte(value))
	return MarkerPrefix + hex.EncodeToString(sum[:])[:12]
}

// Scrub replays the PR side's redaction maps over a baseline render tree, in
// place. It closes the leak window of baselines rendered by an older ATLAS:
// versions before per-release twin loads redacted hierarchy-level SOPS
// values only, so template-level SOPS values could reach the rendered output.
// The current side's redaction map (side-dumped by the atlas-redact plugin as
// <mapDir>/<cluster…>/<deployment>/<release>.json) is replayed over the old
// render: every scalar that equals a map key is replaced by its map value.
//
// Before the map replay, every value of a v1 Secret's data/stringData becomes
// its sha256 marker (the structural pass of atlas-redact.sh, kept in sync
// with it). Hashing the real baseline value makes the marker equal to the
// current side's, so an unchanged Secret does not show in the diff. Values
// already in marker form stay as they are (an already redacted baseline
// would otherwise be hashed twice and every Secret would show as changed).
//
// A release without a map is reported "no-map" and left untouched; the diff
// suppresses it (nothing is known about what it might leak). A file that
// does not parse is left untouched.
func Scrub(baselineRoot, mapDir string, warn io.Writer) ([]ScrubStatus, error) {
	if st, err := os.Stat(baselineRoot); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("scrub: baseline root not a directory: %s", baselineRoot)
	}
	releases, err := Releases(baselineRoot)
	if err != nil {
		return nil, err
	}
	var out []ScrubStatus
	for _, rel := range releases {
		mapFile := filepath.Join(mapDir, filepath.FromSlash(rel)+".json")
		data, err := os.ReadFile(mapFile)
		if err != nil {
			out = append(out, ScrubStatus{"no-map", rel})
			continue
		}
		repl := map[string]any{}
		if err := json.Unmarshal(data, &repl); err != nil {
			fmt.Fprintf(warn, "scrub: unreadable map for %s (%v) — skipping\n", rel, err)
			out = append(out, ScrubStatus{"no-map", rel})
			continue
		}
		dir := filepath.Join(baselineRoot, filepath.FromSlash(rel))
		files, err := ResourceFiles(dir)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			out = append(out, ScrubStatus{"no-yaml", rel})
			continue
		}
		for _, f := range files {
			if err := scrubFile(filepath.Join(dir, filepath.FromSlash(f)), repl); err != nil {
				fmt.Fprintf(warn, "scrub: %s/%s left unchanged: %v\n", rel, f, err)
			}
		}
		out = append(out, ScrubStatus{"scrubbed", rel})
	}
	return out, nil
}

func scrubFile(path string, repl map[string]any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	docs, err := decodeAll(data)
	if err != nil {
		return err
	}
	changed := false
	for _, d := range docs {
		if len(d.Content) == 0 {
			continue
		}
		root := d.Content[0]
		if isV1Secret(root) {
			changed = redactSecretValues(root) || changed
		}
		changed = replayMap(root, repl) || changed
	}
	if !changed {
		return nil // untouched files keep their bytes
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	for _, d := range docs {
		if err := enc.Encode(d); err != nil {
			return err
		}
	}
	if err := enc.Close(); err != nil {
		return err
	}
	tmp := path + ".scrub"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func decodeAll(data []byte) ([]*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var docs []*yaml.Node
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			return docs, nil
		}
		if err != nil {
			return nil, err
		}
		docs = append(docs, &n)
	}
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func isV1Secret(doc *yaml.Node) bool {
	kind, api := mapValue(doc, "kind"), mapValue(doc, "apiVersion")
	return kind != nil && api != nil && kind.Value == "Secret" && api.Value == "v1"
}

// redactSecretValues turns every data/stringData value into its marker.
func redactSecretValues(doc *yaml.Node) bool {
	changed := false
	for _, field := range []string{"data", "stringData"} {
		m := mapValue(doc, field)
		if m == nil || m.Kind != yaml.MappingNode {
			continue
		}
		for i := 1; i < len(m.Content); i += 2 {
			v := m.Content[i]
			if v.Kind == yaml.ScalarNode && v.Tag == "!!null" {
				continue
			}
			s := scalarString(v)
			if strings.HasPrefix(s, MarkerPrefix) {
				continue
			}
			setString(v, Marker(s))
			changed = true
		}
	}
	return changed
}

// replayMap replaces every string or number scalar (not mapping keys) whose
// text is a key of repl — whole-scalar matches only, as atlas-redact.sh does.
func replayMap(n *yaml.Node, repl map[string]any) bool {
	if len(repl) == 0 {
		return false
	}
	changed := false
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag != "!!str" && n.Tag != "!!int" && n.Tag != "!!float" {
			return false
		}
		if v, ok := repl[n.Value]; ok {
			setAny(n, v)
			return true
		}
	case yaml.MappingNode:
		for i := 1; i < len(n.Content); i += 2 {
			changed = replayMap(n.Content[i], repl) || changed
		}
	case yaml.SequenceNode, yaml.DocumentNode:
		for _, c := range n.Content {
			changed = replayMap(c, repl) || changed
		}
	}
	return changed
}

// scalarString is the text of a node (scalars: as written; others: YAML).
func scalarString(n *yaml.Node) string {
	if n.Kind == yaml.ScalarNode {
		return n.Value
	}
	out, err := yaml.Marshal(n)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(string(out), "\n")
}

func setString(n *yaml.Node, s string) {
	*n = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s, Line: n.Line, Column: n.Column}
}

func setAny(n *yaml.Node, v any) {
	switch x := v.(type) {
	case string:
		setString(n, x)
	case bool:
		*n = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(x)}
	case float64:
		if x == float64(int64(x)) {
			*n = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(int64(x), 10)}
		} else {
			*n = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: strconv.FormatFloat(x, 'g', -1, 64)}
		}
	case nil:
		*n = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	default:
		out, _ := json.Marshal(x)
		setString(n, string(out))
	}
}
