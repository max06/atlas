package renderer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/helmfile/chartify"
	"go.yaml.in/yaml/v3"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/cli/values"
	"helm.sh/helm/v4/pkg/downloader"
	"helm.sh/helm/v4/pkg/getter"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

// The toolchain the renderer stands in for: chartify asks the versions to
// pick its command forms, the oracle renders ran with these.
const (
	emulatedHelmVersion      = "v4.3.0+g0000000"
	emulatedKustomizeVersion = "v5.8.3"
)

// chartify turns a release that helmfile hands to chartify (a directory of
// manifests or a kustomization, or a chart with jsonPatches,
// strategicMergePatches or transformers) into a temporary chart, like
// helmfile's PrepareChartify + processChartification. The chartify library
// itself does the work; its helm and kustomize commands run in-process
// (runCommand): helm through the SDK, kustomize through krusty.
func (r *Renderer) chartify(st *State, rel Release, stateDir, chartRef string, local bool, valuesFiles []string) (string, error) {
	// helmfile pulls an OCI chart before chartify (getOCIChart): chartify
	// gets a local directory, and the dependencies stay skipped (the chart
	// is remote for helmfile's skipDeps)
	skipDeps := !local
	if strings.HasPrefix(chartRef, "oci://") {
		dir, err := r.pullUntar(chartRef, rel.Version)
		if err != nil {
			return "", err
		}
		chartRef, local = dir, true
	}
	opts := &chartify.ChartifyOpts{
		WorkaroundOutputDirIssue:    true,
		EnableKustomizeAlphaPlugins: true,
		ChartVersion:                rel.Version,
		Namespace:                   rel.Namespace,
		ID:                          releaseID(rel),
		ValuesFiles:                 valuesFiles,
		// helmfile's own --skip-schema-validation (and the release's)
		TemplateArgs: "--skip-schema-validation",
		IncludeCRDs:  true,
		KubeVersion:  st.KubeVersion,
		// remote charts skip the dependency update (helmfile's skipDeps)
		SkipDeps: skipDeps,
	}
	setFlags, err := chartifySetFlags(rel.Set)
	if err != nil {
		return "", err
	}
	opts.SetFlags = setFlags
	if local {
		if gotmpl, err := hasGotmpl(chartRef); err != nil {
			return "", err
		} else if gotmpl {
			return "", fmt.Errorf("chart %s: .gotmpl files in a chartified directory: %w", chartRef, errUnsupported)
		}
		if err := checkFileDependencies(chartRef); err != nil {
			return "", err
		}
	}
	for _, p := range []struct {
		entries []any
		dst     *[]string
	}{{rel.JSONPatches, &opts.JsonPatches}, {rel.StrategicMergePatches, &opts.StrategicMergePatches}, {rel.Transformers, &opts.Transformers}} {
		for i, e := range p.entries {
			f, err := r.entryFile(stateDir, e)
			if err != nil {
				return "", fmt.Errorf("patch %d: %w", i, err)
			}
			*p.dst = append(*p.dst, f)
		}
	}
	bin := filepath.Join(r.work, "bin")
	runner := chartify.New(
		chartify.HelmBin(filepath.Join(bin, "helm")),
		chartify.KustomizeBin(filepath.Join(bin, "kustomize")),
		chartify.UseHelm4(true),
		chartify.WithLogf(func(string, ...any) {}),
	)
	runner.RunCommand = r.runCommand
	runner.MakeTempDir = func(_, _ string, _ *chartify.ChartifyOpts) string {
		d, err := os.MkdirTemp(r.work, "chartify-")
		if err != nil {
			panic(err)
		}
		return d
	}
	return runner.Chartify(rel.Name, chartRef, chartify.WithChartifyOpts(opts))
}

// releaseID is helmfile's ReleaseToID (chartify's temporary directory id).
func releaseID(rel Release) string {
	id := ""
	if rel.KubeContext != "" {
		id += rel.KubeContext + "/"
	}
	if rel.Namespace != "" {
		id += rel.Namespace + "/"
	}
	if rel.KubeContext != "" && rel.Namespace == "" {
		id += "/"
	}
	return id + rel.Name
}

// chartifySetFlags is helmfile's setFlags(release.SetValues) as chartify
// gets them: flag and value pairs (setString does not reach chartify).
func chartifySetFlags(set []SetValue) ([]string, error) {
	o, err := setOptions(Release{Set: set})
	if err != nil {
		return nil, err
	}
	var flags []string
	for _, sv := range set {
		switch {
		case sv.Value != "", len(sv.Values) > 0:
			flags = append(flags, "--set", o.Values[0])
			o.Values = o.Values[1:]
		case sv.File != "":
			flags = append(flags, "--set-file", o.FileValues[0])
			o.FileValues = o.FileValues[1:]
		}
	}
	return flags, nil
}

var valsRef = regexp.MustCompile(`ref\+.*`)

// entryFile writes one patch entry as helmfile does: an inline map as YAML,
// a path as its content (templates and vals references are refused).
func (r *Renderer) entryFile(stateDir string, e any) (string, error) {
	data, err := entryBytes(stateDir, e)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(r.work, "entry-*.yaml")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return "", err
	}
	return f.Name(), nil
}

func entryBytes(stateDir string, e any) ([]byte, error) {
	switch tv := e.(type) {
	case string:
		path := tv
		if !filepath.IsAbs(path) {
			path = filepath.Join(stateDir, path)
		}
		if strings.HasSuffix(path, ".gotmpl") {
			return nil, fmt.Errorf("%s: template files: %w", tv, errUnsupported)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to render values files \"%s\": %w", tv, err)
		}
		if valsRef.Match(b) {
			return nil, fmt.Errorf("%s: vals references (ref+): %w", tv, errUnsupported)
		}
		return b, nil
	case map[string]any:
		var b bytes.Buffer
		enc := yaml.NewEncoder(&b)
		enc.SetIndent(2)
		if err := enc.Encode(tv); err != nil {
			return nil, err
		}
		enc.Close()
		return b.Bytes(), nil
	}
	return nil, fmt.Errorf("unsupported entry %T", e)
}

func hasGotmpl(dir string) (bool, error) {
	found := false
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".gotmpl") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found, err
}

// checkFileDependencies refuses relative file:// dependencies: helmfile
// rewrites them before chartify (rewriteChartDependencies), not ported yet.
func checkFileDependencies(dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, "Chart.yaml"))
	if err != nil {
		return nil // a directory of manifests
	}
	var meta struct {
		Dependencies []struct {
			Repository string `yaml:"repository"`
		} `yaml:"dependencies"`
	}
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return err
	}
	for _, d := range meta.Dependencies {
		if strings.HasPrefix(d.Repository, "file://") {
			return fmt.Errorf("chart %s: file:// dependencies with patches: %w", dir, errUnsupported)
		}
	}
	return nil
}

// runCommand serves the commands chartify runs.
func (r *Renderer) runCommand(name string, args []string, dir string, stdout, stderr io.Writer, _ map[string]string) error {
	switch filepath.Base(name) {
	case "helm":
		return r.helmCommand(args, dir, stdout)
	case "kustomize":
		return kustomizeCommand(args, dir, stdout)
	}
	return fmt.Errorf("chartify: unexpected command %s", name)
}

func (r *Renderer) helmCommand(args []string, dir string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("chartify: helm without arguments")
	}
	switch args[0] {
	case "version":
		_, err := io.WriteString(stdout, emulatedHelmVersion)
		return err
	case "template":
		return r.chartifyTemplate(args[1:], stdout)
	case "pull":
		return r.chartifyPull(args[1:])
	case "dependency":
		if len(args) == 3 && (args[1] == "up" || args[1] == "build") {
			return r.dependencyUpdate(args[2], args[1] == "build")
		}
	}
	return fmt.Errorf("chartify: unsupported helm command %v", args)
}

// chartifyTemplate is chartify's `helm template --debug=false
// --output-dir=<dir> [--include-crds] <name> <chart> [flags]` (the command
// line split at spaces).
func (r *Renderer) chartifyTemplate(args []string, stdout io.Writer) error {
	t := templateCall{Local: true, Values: &values.Options{}}
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("chartify: helm template flag %s needs a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		var v string
		switch {
		case a == "--debug=false", a == "--include-crds", a == "--skip-schema-validation", a == "":
		case strings.HasPrefix(a, "--output-dir="):
			t.OutputDir = strings.TrimPrefix(a, "--output-dir=")
		case a == "--set":
			if v, err = next(); err == nil {
				t.Values.Values = append(t.Values.Values, v)
			}
		case a == "--set-file":
			if v, err = next(); err == nil {
				t.Values.FileValues = append(t.Values.FileValues, v)
			}
		case a == "-f":
			if v, err = next(); err == nil {
				t.Values.ValueFiles = append(t.Values.ValueFiles, v)
			}
		case a == "--namespace":
			t.Namespace, err = next()
		case a == "--kube-version":
			t.KubeVersion, err = next()
		case a == "--api-versions":
			if v, err = next(); err == nil {
				t.APIVersions = append(t.APIVersions, v)
			}
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("chartify: unsupported helm template flag %s", a)
		default:
			positional = append(positional, a)
		}
		if err != nil {
			return err
		}
	}
	if len(positional) != 2 || t.OutputDir == "" {
		return fmt.Errorf("chartify: unexpected helm template call %v", args)
	}
	t.Name, t.Chart = positional[0], positional[1]
	return r.helmTemplate(context.Background(), t, stdout)
}

// chartifyPull is `helm pull <chart> --untar -d <dir> [--version v]`.
func (r *Renderer) chartifyPull(args []string) error {
	if len(args) < 4 || args[1] != "--untar" || args[2] != "-d" {
		return fmt.Errorf("chartify: unsupported helm pull %v", args)
	}
	version := ""
	if len(args) == 6 && args[4] == "--version" {
		version = args[5]
	} else if len(args) != 4 {
		return fmt.Errorf("chartify: unsupported helm pull %v", args)
	}
	return r.pull(args[0], version, args[3])
}

// pull is `helm pull <chart> --untar -d <dir> [--version v]`: the chart
// directory lands in dir, the archive in a temporary directory.
func (r *Renderer) pull(chartRef, version, dir string) error {
	p := action.NewPull(action.WithConfig(&action.Configuration{RegistryClient: r.registry}))
	p.Settings = r.settings
	p.Untar = true
	p.UntarDir = "."
	p.DestDir = dir
	p.Version = version
	_, err := p.Run(chartRef)
	return err
}

// pullUntar pulls a chart into a new directory and returns the chart
// directory in it.
func (r *Renderer) pullUntar(chartRef, version string) (string, error) {
	dir, err := os.MkdirTemp(r.work, "pull-")
	if err != nil {
		return "", err
	}
	if err := r.pull(chartRef, version, dir); err != nil {
		return "", err
	}
	// helm's untar also creates an (empty) directory named after the
	// reference when the chart has another name: take the chart
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var charts []string
	for _, e := range entries {
		if ok, _ := exists(filepath.Join(dir, e.Name(), "Chart.yaml")); e.IsDir() && ok {
			charts = append(charts, filepath.Join(dir, e.Name()))
		}
	}
	if len(charts) != 1 {
		return "", fmt.Errorf("pull %s: expected one chart directory in %s, found %d", chartRef, dir, len(charts))
	}
	return charts[0], nil
}

// dependencyUpdate is helm dependency up/build of chartify's temporary chart.
func (r *Renderer) dependencyUpdate(dir string, build bool) error {
	ch, err := loader.Load(dir)
	if err != nil {
		return err
	}
	ac, err := chart.NewAccessor(ch)
	if err != nil {
		return err
	}
	if len(ac.MetaDependencies()) == 0 {
		return nil
	}
	m := &downloader.Manager{
		Out: io.Discard, ChartPath: dir, Getters: getter.All(r.settings),
		RepositoryConfig: r.settings.RepositoryConfig, RepositoryCache: r.settings.RepositoryCache,
		ContentCache: r.settings.ContentCache, RegistryClient: r.registry,
	}
	if build {
		return m.Build()
	}
	return m.Update()
}

// kustomizeCommand serves chartify's kustomize calls with krusty, the
// library of the kustomize release the oracle used (api v0.21.3 =
// kustomize v5.8.3), with the options its build command derives from the
// flags.
func kustomizeCommand(args []string, dir string, stdout io.Writer) error {
	if len(args) == 1 && args[0] == "version" {
		_, err := io.WriteString(stdout, emulatedKustomizeVersion)
		return err
	}
	if len(args) >= 4 && args[0] == "edit" && args[1] == "set" && args[2] == "namespace" && len(args) == 4 {
		return kustomizationSet(dir, "namespace", args[3])
	}
	output, path, helmCommand := "", "", "helm"
	alpha, enableHelm, build := false, false, false
	restrictions := types.LoadRestrictionsRootOnly
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-o" || a == "--output":
			if i+1 >= len(args) {
				return fmt.Errorf("chartify: kustomize %s needs a value", a)
			}
			i++
			output = args[i]
		case a == "build":
			build = true
		case a == "--enable-alpha-plugins":
			alpha = true
		case a == "--enable-helm":
			enableHelm = true
		case strings.HasPrefix(a, "--helm-command="):
			helmCommand = strings.TrimPrefix(a, "--helm-command=")
		case a == "--load-restrictor=LoadRestrictionsNone", a == "--load-restrictor=none":
			restrictions = types.LoadRestrictionsNone
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("chartify: unsupported kustomize flag %s", a)
		default:
			path = a
		}
	}
	if !build || output == "" || path == "" {
		return fmt.Errorf("chartify: unsupported kustomize command %v", args)
	}
	// kustomize build's HonorKustomizeFlags: --enable-helm only counts
	// without plugins; the helm command is set either way (a helmCharts
	// inflation would run this binary as helm and fail loudly)
	opts := krusty.MakeDefaultOptions()
	opts.Reorder = krusty.ReorderOptionUnspecified
	opts.LoadRestrictions = restrictions
	if alpha {
		c := types.EnabledPluginConfig(types.BploUseStaticallyLinked)
		c.FnpLoadingOptions = types.FnPluginLoadingOptions{NetworkName: "bridge"}
		opts.PluginConfig = c
	} else {
		opts.PluginConfig.HelmConfig.Enabled = enableHelm
	}
	opts.PluginConfig.HelmConfig.Command = helmCommand
	opts.AddManagedbyLabel = false
	fSys := filesys.MakeFsOnDisk()
	m, err := krusty.MakeKustomizer(opts).Run(fSys, path)
	if err != nil {
		return err
	}
	yml, err := m.AsYaml()
	if err != nil {
		return err
	}
	return fSys.WriteFile(output, yml)
}

// kustomizationSet is `kustomize edit set <field> <value>` on the
// kustomization.yaml in dir.
func kustomizationSet(dir, field, value string) error {
	file := filepath.Join(dir, "kustomization.yaml")
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var k map[string]any
	if err := yaml.Unmarshal(data, &k); err != nil {
		return err
	}
	if k == nil {
		k = map[string]any{}
	}
	k[field] = value
	out, err := yaml.Marshal(k)
	if err != nil {
		return err
	}
	return os.WriteFile(file, out, 0o644)
}
