// Package gha writes GitHub Actions step outputs and workflow commands.
//
// Outside a runner the same calls still work: outputs go to the file the
// caller names (tests) or nowhere, workflow commands are plain stdout lines.
package gha

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// Outputs appends step outputs to a GITHUB_OUTPUT file.
type Outputs struct {
	Path string // "" = discard
}

// FromEnv uses $GITHUB_OUTPUT.
func FromEnv() Outputs { return Outputs{Path: os.Getenv("GITHUB_OUTPUT")} }

// Set appends key=value. A value with a newline uses the heredoc form
// (key<<ATLAS_EOF … ATLAS_EOF), as comment.sh did for the body.
func (o Outputs) Set(key, value string) error {
	if strings.Contains(value, "\n") {
		d := delimiter(value)
		return o.append(key + "<<" + d + "\n" + value + "\n" + d + "\n")
	}
	return o.append(key + "=" + value + "\n")
}

// SetRaw appends key<<ATLAS_EOF, value as is (no newline added), ATLAS_EOF.
// comment.sh wrote the body this way: its last line ends with a newline.
func (o Outputs) SetRaw(key, value string) error {
	if !strings.HasSuffix(value, "\n") {
		value += "\n" // the delimiter must start a line of its own
	}
	d := delimiter(value)
	return o.append(key + "<<" + d + "\n" + value + d + "\n")
}

// delimiter is ATLAS_EOF, unless the value has a line of that text (render
// logs end up in the comment body): then a random one, so the value cannot
// end the output early and set other outputs.
func delimiter(value string) string {
	d := "ATLAS_EOF"
	for containsLine(value, d) {
		var b [8]byte
		_, _ = rand.Read(b[:])
		d = "ATLAS_EOF_" + hex.EncodeToString(b[:])
	}
	return d
}

func containsLine(s, line string) bool {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimRight(l, "\r") == line {
			return true
		}
	}
	return false
}

// Setf is Set with a formatted value.
func (o Outputs) Setf(key, format string, args ...any) error {
	return o.Set(key, fmt.Sprintf(format, args...))
}

func (o Outputs) append(s string) error {
	if o.Path == "" || o.Path == os.DevNull {
		return nil
	}
	f, err := os.OpenFile(o.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(s); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Log prints workflow commands (::error:: etc.) and plain lines.
type Log struct {
	W io.Writer
}

// Annotate prints ::<level>::msg (level error, warning or notice).
func (l Log) Annotate(level, format string, args ...any) {
	fmt.Fprintf(l.W, "::%s::%s\n", level, fmt.Sprintf(format, args...))
}

// Printf prints a plain line.
func (l Log) Printf(format string, args ...any) {
	fmt.Fprintf(l.W, format+"\n", args...)
}

// Group prints a collapsible log group around fn.
func (l Log) Group(title string, fn func()) {
	fmt.Fprintf(l.W, "::group::%s\n", title)
	fn()
	fmt.Fprintln(l.W, "::endgroup::")
}
