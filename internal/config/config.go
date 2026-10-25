// Package config reads atlas.yaml: the file that marks the root of an ATLAS
// tree and pins the engine that renders it.
//
// The pin is plain YAML on purpose. Each branch carries its own copy, so a PR
// or a local edit of one value tests another engine before it reaches a
// cluster, and every tool that must pick an engine (the review, the developer
// wrapper, the Argo CD plugin discovery) reads the same field.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"gopkg.in/yaml.v3"
)

const (
	// FileName is the file at the root of the tree (the Argo CD source path).
	FileName = "atlas.yaml"
	// APIVersion and Kind identify the file; nothing else may be at the top level
	// besides metadata and spec.
	APIVersion = "atlas.io/v1alpha1"
	Kind       = "Root"

	// Defaults of the directory keys: the values consumer entry helmfiles pass
	// as atlas.appTemplates and atlas.deploymentDefinitions.
	DefaultTemplateDir   = "templates"
	DefaultDeploymentDir = "deployments"
)

// ErrNotFound means the directory has no atlas.yaml (errors.Is also matches
// fs.ErrNotExist).
var ErrNotFound = fmt.Errorf("no %s: %w", FileName, fs.ErrNotExist)

// Root is the content of atlas.yaml.
type Root struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata,omitempty"`
	Spec       Spec     `yaml:"spec"`
}

// Metadata is optional.
type Metadata struct {
	Name string `yaml:"name,omitempty"`
}

// Spec holds the pin and the tree layout.
type Spec struct {
	// Engine is the engine minor that renders this tree.
	Engine Engine `yaml:"engine"`
	// TemplateDir and DeploymentDir are relative to the file's directory.
	TemplateDir   string `yaml:"templateDir,omitempty"`
	DeploymentDir string `yaml:"deploymentDir,omitempty"`
}

// Engine is an engine minor, "<major>.<minor>". Patch releases of one minor
// render the same; a minor is the unit that gets its own Argo CD plugin.
type Engine struct {
	Major, Minor int
	set          bool // decoded from the file ("0.0" is a value, not a missing key)
}

var (
	engineRe  = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	releaseRe = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
)

// ParseEngine parses "<major>.<minor>".
func ParseEngine(s string) (Engine, error) {
	m := engineRe.FindStringSubmatch(s)
	if m == nil {
		return Engine{}, fmt.Errorf("engine %q is not <major>.<minor> (for example \"0.13\")", s)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return Engine{Major: major, Minor: minor, set: true}, nil
}

// EngineOfVersion derives the engine minor of a release version ("v0.13.2",
// "0.13.2"). ok is false for builds without a release version (dev, branch
// images).
func EngineOfVersion(version string) (Engine, bool) {
	m := releaseRe.FindStringSubmatch(version)
	if m == nil {
		return Engine{}, false
	}
	e, err := ParseEngine(m[1] + "." + m[2])
	return e, err == nil
}

func (e Engine) String() string { return fmt.Sprintf("%d.%d", e.Major, e.Minor) }

// Plugin is the name of the Argo CD config management plugin that serves this
// minor: "atlas-v<major>-<minor>" (plugin name "atlas", version
// "v<major>-<minor>"; the repo-server socket is "<plugin>.sock").
func (e Engine) Plugin() string { return fmt.Sprintf("atlas-v%d-%d", e.Major, e.Minor) }

// UnmarshalYAML accepts only a quoted string: an unquoted 1.10 is the float
// 1.1 for every YAML reader, so it would select another engine than written.
func (e *Engine) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return fmt.Errorf("line %d: spec.engine must be a quoted string (\"1.10\", not 1.10), found %s %s", node.Line, node.Tag, node.Value)
	}
	parsed, err := ParseEngine(node.Value)
	if err != nil {
		return fmt.Errorf("line %d: spec.engine: %w", node.Line, err)
	}
	*e = parsed
	return nil
}

// MarshalYAML writes the quoted form.
func (e Engine) MarshalYAML() (any, error) {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Style: yaml.DoubleQuotedStyle, Value: e.String()}, nil
}

// Load reads dir/atlas.yaml. It returns ErrNotFound when the file does not
// exist, and an error that names the file for anything it does not accept.
func Load(dir string) (*Root, error) {
	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	return r, nil
}

// Parse decodes and validates the content of atlas.yaml. Unknown keys are
// errors: a misspelled key must not fall back to a default silently.
func Parse(data []byte) (*Root, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var r Root
	if err := dec.Decode(&r); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the file is empty")
		}
		return nil, err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("the file must hold exactly one YAML document")
	}
	if r.APIVersion != APIVersion || r.Kind != Kind {
		return nil, fmt.Errorf("apiVersion/kind is %q/%q, this atlas reads %s/%s", r.APIVersion, r.Kind, APIVersion, Kind)
	}
	if !r.Spec.Engine.set {
		return nil, errors.New("spec.engine is required (for example engine: \"0.13\")")
	}
	for key, dir := range map[string]*string{"templateDir": &r.Spec.TemplateDir, "deploymentDir": &r.Spec.DeploymentDir} {
		if *dir == "" {
			continue
		}
		if !filepath.IsLocal(*dir) {
			return nil, fmt.Errorf("spec.%s %q must be a relative path inside the tree", key, *dir)
		}
		*dir = filepath.Clean(*dir)
	}
	if r.Spec.TemplateDir == "" {
		r.Spec.TemplateDir = DefaultTemplateDir
	}
	if r.Spec.DeploymentDir == "" {
		r.Spec.DeploymentDir = DefaultDeploymentDir
	}
	return &r, nil
}
