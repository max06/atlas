// Package templates embeds the helmfile templates of ATLAS for the native
// engine (internal/engine).
//
// helmfile renders the atlas templates with this directory as base path:
// every _*.tpl in it is a named template of each render (also of `tpl`
// calls on consumer files), relative paths of readFile/isFile resolve in it
// and exec runs in it. The native engine extracts the files into a real
// directory and uses it as its base path, so all of that stays the same.
package templates

import "embed"

// Files are the template files of this directory.
//
//go:embed *.gotmpl *.yaml _*.tpl
var Files embed.FS
