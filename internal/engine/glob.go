package engine

import (
	"errors"
	"os"
	"path"
	"regexp"
	"strings"
)

// The glob of templates/_glob.tpl, ported step by step: discovery must match
// the template engine exactly, including its corner cases (a `*` is `.*`
// and matches every entry type; an empty path part is skipped, a "." part is
// kept; recursion follows only real directories, existence checks follow
// symlinks). Paths are built as strings, never cleaned.

// isFile and isDir are helmfile's: "does not exist" is false, any other stat
// error is an error (helmfile's template fails on it).
func isFile(path string) (bool, error) {
	st, err := os.Stat(path)
	if err == nil {
		return !st.IsDir(), nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func isDir(path string) (bool, error) {
	st, err := os.Stat(path)
	if err == nil {
		return st.IsDir(), nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// wildcard turns a path part with `*` (and `?`) into the template's regex.
// sprig's regexMatch drops compile errors: an invalid pattern matches
// nothing (a nil regex here).
func wildcard(part string) (*regexp.Regexp, error) {
	re := strings.ReplaceAll(part, "*", ".*")
	re = strings.ReplaceAll(re, "?", ".")
	compiled, err := regexp.Compile("^" + re + "$")
	if err != nil {
		return nil, nil
	}
	return compiled, nil
}

func matches(re *regexp.Regexp, name string) bool { return re != nil && re.MatchString(name) }

// glob is `include "glob" pattern | fromJson`.
func glob(pattern string) ([]string, error) {
	if strings.Contains(pattern, "**") {
		parts := strings.Split(pattern, "**")
		prefix := strings.TrimSuffix(parts[0], "/")
		suffix := ""
		if len(parts) > 1 {
			suffix = strings.TrimPrefix(parts[1], "/")
		}
		start := prefix
		if start == "" {
			start = "."
		}
		return globRecursive(start, suffix)
	}
	parts := strings.Split(pattern, "/")
	for i, p := range parts {
		if strings.Contains(p, "*") && i < len(parts)-1 {
			return globIterative(parts, 0, "")
		}
	}
	dir, base := path.Dir(pattern), path.Base(pattern) // sprig dir/base
	if dir == "." {
		dir = ""
	}
	results := []string{}
	ok := dir == ""
	if !ok {
		var err error
		if ok, err = isDir(dir); err != nil {
			return nil, err
		}
	}
	if !ok {
		return results, nil
	}
	entries, err := os.ReadDir(orDot(dir))
	if err != nil {
		return nil, err
	}
	var re *regexp.Regexp
	if strings.Contains(base, "*") {
		if re, err = wildcard(base); err != nil {
			return nil, err
		}
	}
	for _, e := range entries {
		full := e.Name()
		if dir != "" {
			full = dir + "/" + e.Name()
		}
		if (strings.Contains(base, "*") && matches(re, e.Name())) || (!strings.Contains(base, "*") && base == e.Name()) {
			results = append(results, full)
		}
	}
	return results, nil
}

func orDot(dir string) string {
	if dir == "" {
		return "."
	}
	return dir
}

// globIterative is the template's "globIterative".
func globIterative(parts []string, index int, current string) ([]string, error) {
	results := []string{}
	if index >= len(parts) {
		f, err := isFile(current)
		if err != nil {
			return nil, err
		}
		d, err := isDir(current)
		if err != nil {
			return nil, err
		}
		if f || d {
			results = append(results, current)
		}
		return results, nil
	}
	part := parts[index]
	last := index == len(parts)-1
	join := func(name string) string {
		switch current {
		case "":
			return name
		case "/":
			return "/" + name
		}
		return current + "/" + name
	}
	if part == "" {
		next := current
		if index == 0 && current == "" {
			next = "/"
		}
		return globIterative(parts, index+1, next)
	}
	search := current
	if search == "" {
		search = "."
	}
	ok, err := isDir(search)
	if err != nil || !ok {
		return results, err
	}
	entries, err := os.ReadDir(search)
	if err != nil {
		return nil, err
	}
	if strings.Contains(part, "*") {
		re, err := wildcard(part)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !matches(re, e.Name()) {
				continue
			}
			p := join(e.Name())
			if last {
				results = append(results, p)
				continue
			}
			sub, err := globIterative(parts, index+1, p)
			if err != nil {
				return nil, err
			}
			results = append(results, sub...)
		}
		return results, nil
	}
	p := join(part)
	if !last {
		return globIterative(parts, index+1, p)
	}
	f, err := isFile(p)
	if err != nil {
		return nil, err
	}
	d, err := isDir(p)
	if err != nil {
		return nil, err
	}
	if f || d {
		results = append(results, p)
	}
	return results, nil
}

// globRecursive is the template's "globRecursive": every entry below dir
// that matches pattern, depth first, entries in name order.
func globRecursive(dir, pattern string) ([]string, error) {
	results := []string{}
	ok, err := isDir(dir)
	if err != nil || !ok {
		return results, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		var full string
		switch {
		case strings.HasPrefix(dir, "/"):
			full = strings.TrimSuffix(dir, "/") + "/" + e.Name()
		case dir == ".":
			full = e.Name()
		default:
			full = strings.TrimSuffix(dir, "/") + "/" + e.Name()
		}
		if pattern == "" {
			results = append(results, full)
			if e.IsDir() {
				sub, err := globRecursive(full, "")
				if err != nil {
					return nil, err
				}
				results = append(results, sub...)
			}
			continue
		}
		parts := strings.Split(pattern, "/")
		inPath := false
		for i, p := range parts {
			if strings.Contains(p, "*") && i < len(parts)-1 {
				inPath = true
			}
		}
		if inPath {
			sub, err := globIterative(parts, 0, full)
			if err != nil {
				return nil, err
			}
			results = append(results, sub...)
		} else {
			first := parts[0]
			match := false
			if strings.Contains(first, "*") {
				re, err := wildcard(first)
				if err != nil {
					return nil, err
				}
				match = matches(re, e.Name())
			} else {
				match = first == e.Name()
			}
			if match && len(parts) == 1 {
				results = append(results, full)
			}
		}
		if e.IsDir() {
			sub, err := globRecursive(full, pattern)
			if err != nil {
				return nil, err
			}
			results = append(results, sub...)
		}
	}
	return results, nil
}
