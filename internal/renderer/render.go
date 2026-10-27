package renderer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/cli/values"
	"helm.sh/helm/v4/pkg/getter"
	"helm.sh/helm/v4/pkg/postrenderer"
	"helm.sh/helm/v4/pkg/registry"
	release "helm.sh/helm/v4/pkg/release/v1"
	repo "helm.sh/helm/v4/pkg/repo/v1"
)

var errUnsupported = errors.New("not supported yet")

// Options of a render.
type Options struct {
	// OutputDir: write the manifests like helm template --output-dir, into
	// <OutputDir>/<cluster>/<deployment>/<release> (helmfile's output dir
	// template of the atlas CLI). Empty: write them to Stdout.
	OutputDir string
	Stdout    io.Writer
	// Log gets helm's progress lines (helmfile's stderr).
	Log io.Writer
	// NamespaceOverride is helmfile's --namespace.
	NamespaceOverride string
	// OutputDirTemplate is helmfile's --output-dir-template (empty: the
	// helmfile default).
	OutputDirTemplate string
	// Selectors are helmfile's --selector flags (OR of the selectors).
	Selectors []Selector
}

// Renderer renders generated states with one helm environment.
type Renderer struct {
	opts     Options
	settings *cli.EnvSettings
	work     string // repositories.yaml, repository index cache, temporary files
	repos    map[string]Repository
	registry *registry.Client
	added    map[string]bool
}

// Render renders every release of the root, state by state, release by
// release (helmfile template's order).
func Render(ctx context.Context, root *Root, o Options) error {
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	work, err := os.MkdirTemp("", "atlas-render-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	settings := cli.New()
	// an own repository list: the states name their repositories, the
	// user's helm configuration plays no part (helmfile adds them anew too)
	settings.RepositoryConfig = filepath.Join(work, "repositories.yaml")
	settings.RepositoryCache = filepath.Join(work, "repository")
	rc, err := registry.NewClient(
		registry.ClientOptEnableCache(true),
		registry.ClientOptWriter(o.Log),
		registry.ClientOptCredentialsFile(settings.RegistryConfig),
	)
	if err != nil {
		return fmt.Errorf("registry client: %w", err)
	}
	// chartify looks its helm and kustomize binaries up before it runs them
	// through runCommand: two names for this executable satisfy the lookup,
	// nothing is executed
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(work, "bin"), 0o700); err != nil {
		return err
	}
	for _, name := range []string{"helm", "kustomize"} {
		if err := os.Symlink(self, filepath.Join(work, "bin", name)); err != nil {
			return err
		}
	}
	r := &Renderer{opts: o, settings: settings, work: work, registry: rc, repos: map[string]Repository{}, added: map[string]bool{}}
	matched := 0
	for _, st := range root.States {
		n, err := r.renderState(ctx, root, st)
		if err != nil {
			return err
		}
		matched += n
	}
	if len(o.Selectors) > 0 && matched == 0 {
		return fmt.Errorf("err: no releases found that matches specified selector(%s) and environment(default), in any helmfile", o.selectorText())
	}
	return nil
}

func (o Options) selectorText() string {
	var parts []string
	for _, s := range o.Selectors {
		var conds []string
		for _, c := range s {
			op := "="
			if c.negate {
				op = "!="
			}
			conds = append(conds, c.key+op+c.value)
		}
		parts = append(parts, strings.Join(conds, ","))
	}
	return strings.Join(parts, " | ")
}

func (o Options) selected(labels map[string]string) bool {
	if len(o.Selectors) == 0 {
		return true
	}
	for _, s := range o.Selectors {
		if s.matches(labels) {
			return true
		}
	}
	return false
}

// renderState renders the selected releases of a state and returns their
// number. Selectors see the prepared labels; --namespace replaces the
// namespace before the selection (helmfile's ApplyOverrides at load). Like
// helmfile, all charts of the state are prepared first (chartify included,
// whose output lands in between), then the releases render in order.
func (r *Renderer) renderState(ctx context.Context, root *Root, st *State) (int, error) {
	for _, repo := range st.Repositories {
		r.repos[repo.Name] = repo
	}
	prepared, err := st.Prepared(r.opts.NamespaceOverride)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", st.File, err)
	}
	var calls []templateCall
	defer func() {
		for _, t := range calls {
			for _, f := range t.cleanup {
				os.Remove(f)
			}
		}
	}()
	for _, rel := range prepared {
		if r.opts.NamespaceOverride != "" {
			rel.Namespace = r.opts.NamespaceOverride
			rel.Labels["namespace"] = rel.Namespace
		}
		if !r.opts.selected(rel.Labels) {
			continue
		}
		t, err := r.prepareRelease(root, st, rel)
		calls = append(calls, t)
		if err != nil {
			return len(calls), fmt.Errorf("release %q in %s: %w", rel.Name, st.File, err)
		}
	}
	for _, t := range calls {
		if err := r.templateRelease(ctx, t); err != nil {
			return len(calls), fmt.Errorf("release %q in %s: %w", t.Name, st.File, err)
		}
	}
	return len(calls), nil
}

// prepareRelease is helmfile's prepareChartForRelease plus the template
// flags: the chart reference (repositories added, chartify run), the values
// files and set flags, the output directory.
func (r *Renderer) prepareRelease(root *Root, st *State, rel Release) (templateCall, error) {
	stateDir := filepath.Dir(filepath.Join(root.Dir, st.File))
	t := templateCall{Name: rel.Name, Namespace: rel.Namespace, Version: rel.Version, KubeVersion: st.KubeVersion,
		PostRenderer: rel.PostRenderer, PostRendererArgs: rel.PostRendererArgs}
	chartRef := rel.Chart
	local := filepath.IsAbs(chartRef) || strings.HasPrefix(chartRef, "./") || strings.HasPrefix(chartRef, "../")
	manifestsDir := false
	if local {
		if !filepath.IsAbs(chartRef) {
			chartRef = filepath.Join(stateDir, chartRef)
		}
		ok, err := exists(filepath.Join(chartRef, "Chart.yaml"))
		if err != nil {
			return t, err
		}
		manifestsDir = !ok
	} else {
		var err error
		if chartRef, err = r.remoteRef(chartRef); err != nil {
			return t, err
		}
	}
	files, err := r.valuesFiles(stateDir, rel)
	t.cleanup = files
	if err != nil {
		return t, err
	}
	vo, err := setOptions(rel)
	if err != nil {
		return t, err
	}
	vo.ValueFiles = files
	t.Values = vo
	if manifestsDir || needsChartify(rel) {
		dir, err := r.chartify(st, rel, stateDir, chartRef, local, files)
		if err != nil {
			return t, err
		}
		chartRef, local = dir, true
	}
	t.Chart, t.Local = chartRef, local
	if r.opts.OutputDir != "" {
		if t.OutputDir, err = st.outputDir(root.Dir, r.opts.OutputDir, r.opts.OutputDirTemplate, rel); err != nil {
			return t, err
		}
	}
	return t, nil
}

// templateRelease is helmfile's TemplateRelease: helm's stdout plus a
// newline; with an output directory it is only the "wrote" lines, which go
// to the log.
func (r *Renderer) templateRelease(ctx context.Context, t templateCall) error {
	var out bytes.Buffer
	if err := r.helmTemplate(ctx, t, &out); err != nil {
		return err
	}
	if t.OutputDir != "" {
		_, err := io.WriteString(r.opts.Log, dedupeLines(out.String()))
		return err
	}
	if out.Len() > 0 {
		out.WriteByte('\n')
		_, err := r.opts.Stdout.Write(out.Bytes())
		return err
	}
	return nil
}

// dedupeLines drops repeated lines (helm writes one "wrote" line per
// document of a file).
func dedupeLines(s string) string {
	seen := map[string]bool{}
	var b strings.Builder
	for _, line := range strings.SplitAfter(s, "\n") {
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		b.WriteString(line)
	}
	return b.String()
}

// templateCall is one `helm template` (with --include-crds and
// --skip-schema-validation, the flags of every atlas render).
type templateCall struct {
	Name, Chart        string
	Local              bool // Chart is a directory: helm dependency build if needed
	Namespace, Version string
	KubeVersion        string
	APIVersions        []string
	OutputDir          string
	Values             *values.Options
	// PostRenderer: a helm post-renderer plugin (--post-renderer) and its
	// arguments (--post-renderer-args), loaded from HELM_PLUGINS like helm.
	PostRenderer     string
	PostRendererArgs []string
	// cleanup: temporary files of the call (values)
	cleanup []string
}

// helmTemplate renders like `helm template`: without an output directory the
// manifests and hooks go to out; with one, helm writes the files and out
// gets helm's "wrote <file>" lines (the install action prints them to the
// process stdout, which is captured for the duration of the call).
func (r *Renderer) helmTemplate(ctx context.Context, t templateCall, out io.Writer) error {
	client := action.NewInstall(&action.Configuration{})
	client.DryRunStrategy = action.DryRunClient
	client.ReleaseName = t.Name
	client.Replace = true
	client.IncludeCRDs = true
	client.Namespace = t.Namespace
	if client.Namespace == "" {
		client.Namespace = r.settings.Namespace()
	}
	client.Version = t.Version
	client.SkipSchemaValidation = true
	client.SetRegistryClient(r.registry)
	client.APIVersions = common.VersionSet(t.APIVersions)
	if t.KubeVersion != "" {
		kv, err := common.ParseKubeVersion(t.KubeVersion)
		if err != nil {
			return fmt.Errorf("invalid kube version '%s': %w", t.KubeVersion, err)
		}
		client.KubeVersion = kv
	}
	if t.OutputDir != "" {
		if err := os.MkdirAll(t.OutputDir, 0o755); err != nil {
			return err
		}
		client.OutputDir = t.OutputDir
	}
	if t.PostRenderer != "" {
		pr, err := postrenderer.NewPostRendererPlugin(r.settings, t.PostRenderer, t.PostRendererArgs...)
		if err != nil {
			return err
		}
		client.PostRenderer = pr
	}
	chartPath, err := client.LocateChart(t.Chart, r.settings)
	if err != nil {
		return err
	}
	vals, err := t.Values.MergeValues(getter.All(r.settings))
	if err != nil {
		return err
	}
	ch, err := loader.Load(chartPath)
	if err != nil {
		return err
	}
	ac, err := chart.NewAccessor(ch)
	if err != nil {
		return err
	}
	switch ac.MetadataAsMap()["Type"] {
	case "", "application":
	default:
		return fmt.Errorf("%s charts are not installable", ac.MetadataAsMap()["Type"])
	}
	if req := ac.MetaDependencies(); len(req) > 0 {
		if err := action.CheckDependencies(ch, req); err != nil {
			if !t.Local {
				return fmt.Errorf("an error occurred while checking for chart dependencies. You may need to run 'helm dependency build' to fetch missing dependencies: %w", err)
			}
			// helmfile runs `helm dependency build` on local charts
			if ch, err = r.buildDependencies(chartPath); err != nil {
				return err
			}
		}
	}
	var res any
	wrote, err := captureStdout(func() error {
		var err error
		res, err = client.RunWithContext(ctx, ch, vals)
		return err
	})
	if err != nil {
		return err
	}
	rl, ok := res.(*release.Release)
	if !ok {
		return errors.New("unexpected release type")
	}
	if t.OutputDir == "" {
		var b bytes.Buffer
		fmt.Fprintln(&b, strings.TrimSpace(rl.Manifest))
		for _, h := range rl.Hooks {
			fmt.Fprintf(&b, "---\n# Source: %s\n%s\n", h.Path, h.Manifest)
		}
		_, err := out.Write(b.Bytes())
		return err
	}
	if _, err := io.WriteString(out, wrote); err != nil {
		return err
	}
	written := map[string]bool{}
	for _, h := range rl.Hooks {
		if _, err := os.Stat(filepath.Join(t.OutputDir, h.Path)); err == nil {
			written[h.Path] = true
		}
		if err := writeToFile(t.OutputDir, h.Path, h.Manifest, written[h.Path], out); err != nil {
			return err
		}
	}
	return nil
}

// captureStdout runs fn with the process stdout redirected into a pipe and
// returns what fn printed. Renders run one at a time, so the swap is safe.
func captureStdout(fn func() error) (string, error) {
	rd, wr, err := os.Pipe()
	if err != nil {
		return "", err
	}
	saved := os.Stdout
	os.Stdout = wr
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(rd)
		done <- string(b)
	}()
	ferr := fn()
	os.Stdout = saved
	wr.Close()
	out := <-done
	rd.Close()
	return out, ferr
}

// writeToFile is helm template's hook writer (with its "wrote" line).
func writeToFile(outputDir, name, data string, appendData bool, out io.Writer) error {
	file := outputDir + string(filepath.Separator) + name
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	var f *os.File
	var err error
	if appendData {
		f, err = os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o600)
	} else {
		f, err = os.Create(file)
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = fmt.Fprintf(f, "---\n# Source: %s\n%s\n", name, data); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "wrote %s\n", file)
	return err
}

// setOptions are helmfile's set and setString flags (setFlags,
// setStringFlags) as helm values options: value -> --set(-string), file ->
// --set-file (set only: setString ignores file), values -> name={a,b}.
// Secret references (ref+) need vals and are refused.
func setOptions(rel Release) (*values.Options, error) {
	o := &values.Options{}
	item := func(v string) (string, error) {
		if strings.Contains(v, "ref+") {
			return "", fmt.Errorf("set value with a secret reference (ref+) is not supported")
		}
		return escape(v), nil
	}
	for _, list := range []struct {
		entries []SetValue
		dst     *[]string
		files   bool
	}{{rel.Set, &o.Values, true}, {rel.SetString, &o.StringValues, false}} {
		for _, sv := range list.entries {
			switch {
			case sv.Value != "":
				v, err := item(sv.Value)
				if err != nil {
					return nil, err
				}
				*list.dst = append(*list.dst, escape(sv.Name)+"="+v)
			case sv.File != "":
				if list.files {
					o.FileValues = append(o.FileValues, escape(sv.Name)+"="+sv.File)
				}
			case len(sv.Values) > 0:
				items := make([]string, len(sv.Values))
				for i, raw := range sv.Values {
					v, err := item(fmt.Sprint(raw))
					if err != nil {
						return nil, err
					}
					items[i] = v
				}
				*list.dst = append(*list.dst, escape(sv.Name)+"={"+strings.Join(items, ",")+"}")
			}
		}
	}
	return o, nil
}

// escape is helmfile's escape of set names and values.
func escape(value string) string {
	v := strings.ReplaceAll(value, "{", "\\{")
	v = strings.ReplaceAll(v, "}", "\\}")
	return strings.ReplaceAll(v, ",", "\\,")
}

func needsChartify(rel Release) bool {
	return len(rel.JSONPatches) > 0 || len(rel.StrategicMergePatches) > 0 || len(rel.Transformers) > 0
}

// remoteRef resolves <repository>/<chart> against the state repositories:
// an OCI repository becomes oci://<url>/<chart>, a chart repository is
// added to the render's repository list (helm repo add). oci:// passes.
func (r *Renderer) remoteRef(ref string) (string, error) {
	if strings.HasPrefix(ref, "oci://") {
		return ref, nil
	}
	name, chartName, ok := strings.Cut(ref, "/")
	if !ok {
		return "", fmt.Errorf("chart %q: neither a local directory nor <repository>/<chart>", ref)
	}
	rp, ok := r.repos[name]
	if !ok {
		return "", fmt.Errorf("chart %q: no repository %q in the state", ref, name)
	}
	if rp.OCI {
		return "oci://" + strings.TrimSuffix(strings.TrimPrefix(rp.URL, "oci://"), "/") + "/" + chartName, nil
	}
	if err := r.addRepo(rp); err != nil {
		return "", err
	}
	return ref, nil
}

// addRepo is helm repo add: the entry in the repositories file and the
// downloaded index.
func (r *Renderer) addRepo(rp Repository) error {
	if r.added[rp.Name] {
		return nil
	}
	f, err := repo.LoadFile(r.settings.RepositoryConfig)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if f == nil {
		f = repo.NewFile()
	}
	entry := &repo.Entry{Name: rp.Name, URL: rp.URL}
	cr, err := repo.NewChartRepository(entry, getter.All(r.settings))
	if err != nil {
		return err
	}
	cr.CachePath = r.settings.RepositoryCache
	if _, err := cr.DownloadIndexFile(); err != nil {
		return fmt.Errorf("looks like %q is not a valid chart repository or cannot be reached: %w", rp.URL, err)
	}
	f.Update(entry)
	if err := f.WriteFile(r.settings.RepositoryConfig, 0o600); err != nil {
		return err
	}
	r.added[rp.Name] = true
	return nil
}

// valuesFiles writes the release values as helmfile does: an inline map
// becomes a YAML file (go.yaml.in/yaml/v3, indent 2), a path is passed on
// as its content. Mode 0600: the values may hold decrypted secrets.
func (r *Renderer) valuesFiles(stateDir string, rel Release) ([]string, error) {
	var files []string
	for i, v := range rel.Values {
		var data []byte
		switch tv := v.(type) {
		case string:
			path := tv
			if !filepath.IsAbs(path) {
				path = filepath.Join(stateDir, path)
			}
			if strings.HasSuffix(path, ".gotmpl") {
				return files, fmt.Errorf("values[%d]: template values files are not supported", i)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return files, fmt.Errorf("failed to render values files \"%s\": %w", tv, err)
			}
			if valsRef.Match(b) {
				// helmfile would evaluate the file with vals
				return files, fmt.Errorf("values %s: vals references (ref+): %w", tv, errUnsupported)
			}
			data = b
		case map[string]any:
			var b bytes.Buffer
			enc := yaml.NewEncoder(&b)
			enc.SetIndent(2)
			if err := enc.Encode(tv); err != nil {
				return files, err
			}
			enc.Close()
			data = b.Bytes()
		default:
			return files, fmt.Errorf("values[%d]: unsupported entry %T", i, v)
		}
		f, err := os.CreateTemp(r.work, "values-*.yaml")
		if err != nil {
			return files, err
		}
		files = append(files, f.Name())
		if _, err := f.Write(data); err != nil {
			f.Close()
			return files, err
		}
		if err := f.Close(); err != nil {
			return files, err
		}
	}
	return files, nil
}

// buildDependencies is helm dependency build of a local chart, on a copy:
// the consumer tree stays untouched.
func (r *Renderer) buildDependencies(chartPath string) (chart.Charter, error) {
	return nil, fmt.Errorf("chart %s: local chart dependencies are not supported yet", chartPath)
}

func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}
