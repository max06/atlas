// Package templates embeds the helper templates of the helmfile engine.
//
// helmfile loads every _*.tpl of a state file's directory as named templates
// into each template render, also into `tpl` calls on consumer files. The
// native engine (internal/engine) gives its tpl context the same helpers.
package templates

import "embed"

// Helpers are the _*.tpl files of this directory.
//
//go:embed _*.tpl
var Helpers embed.FS
