package report

import (
	"regexp"
	"strconv"
	"strings"
)

// CommentInput is everything the PR comment states.
type CommentInput struct {
	BaselineStatus string // success / error / missing
	PRStatus       string // success / error
	DiffStatus     string // empty / no-changes / changes

	MergeFallback bool
	// Filter support (selector probe) per side; false = an old ATLAS that
	// leaks template-level secrets.
	BaselineFilter, PRFilter bool

	WorkflowPinTarget, WorkflowPinMerge string
	LatestTag, LatestMainSHA            string

	DiffTotal, DiffReleases int
	DiffTruncated           bool
	DiffBlocks              string // per-release blocks for the comment
	SummaryBlocks           string // per-release blocks for the job summary

	Scope string // render-scope fragment ("" = subsetting off)

	BaselineErrors, PRErrors string // tails of the render logs
	HelmfilePath             string
	RunURL                   string
}

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type lines struct{ b strings.Builder }

func (l *lines) emit(s ...string) {
	for _, x := range s {
		l.b.WriteString(x + "\n")
	}
}

// Comment builds the PR comment body.
func Comment(in CommentInput) string {
	var l lines
	l.emit("## ATLAS Review", "")

	// merge-result render error (blocking)
	if in.PRStatus == "error" {
		l.emit("> [!CAUTION]",
			"> **This PR failed to render.** The pipeline will fail — fix the errors below before merging.",
			">",
			"> To diagnose locally, run:",
			">",
			"> ```bash",
			"> helmfile -f "+in.HelmfilePath+" template --skip-schema-validation",
			"> ```",
			">",
			"> If the local render succeeds but CI still fails, please file an issue at <https://github.com/max06/atlas/issues>.",
			"")
		if in.PRErrors != "" {
			l.emit("<details>", "<summary>Render errors (merge result)</summary>", "", "```", in.PRErrors, "```", "", "</details>", "")
		}
	}

	if in.MergeFallback {
		l.emit("> [!WARNING]",
			"> **Merge ref unavailable** — this likely means the PR has merge conflicts with the target branch. The diff below compares the PR branch with its merge-base, so it shows the PR's own changes, not the actual merge result. Please rebase or merge the target branch into your PR.",
			"")
	}

	pinMessages(&l, in)

	// security messaging (selector probe)
	switch {
	case !in.BaselineFilter && !in.PRFilter:
		l.emit("> [!CAUTION]",
			"> **Both sides use an older ATLAS version that leaks template-level secrets.** Values from SOPS files referenced inside app templates (`release[].values`, `release[].secrets`) may appear in cleartext in the diff below. Review carefully and rotate anything that leaks.",
			"")
	case !in.BaselineFilter:
		l.emit("> [!CAUTION]",
			"> **The target branch uses an older ATLAS version that leaks template-level secrets.** Values from SOPS files referenced inside app templates may appear in cleartext on the baseline side of the diff. Review carefully and rotate anything that leaks.",
			"")
	case !in.PRFilter:
		l.emit("> [!CAUTION]",
			"> **This PR pins an older ATLAS version that leaks template-level secrets.** Values from SOPS files referenced inside app templates may appear in cleartext on the PR side of the diff. Review carefully and rotate anything that leaks.",
			"")
	}

	switch in.BaselineStatus {
	case "error":
		l.emit("> [!WARNING]",
			"> **Target branch failed to render.** Diff is unavailable, but merging is not blocked — this PR may be the fix.",
			"")
		if in.BaselineErrors != "" {
			l.emit("<details>", "<summary>Render errors (target branch)</summary>", "", "```", in.BaselineErrors, "```", "", "</details>", "")
		}
	case "missing":
		l.emit("> [!NOTE]",
			"> Helmfile not found on the target branch (`"+in.HelmfilePath+"`). This is expected if the PR introduces ATLAS to the repository.",
			"")
	}

	if in.Scope != "" {
		l.b.WriteString(strings.TrimRight(in.Scope, "\n") + "\n")
		l.emit("")
	}

	switch {
	case in.PRStatus == "error" && in.BaselineStatus != "success":
		l.emit("Unable to generate a diff.")
	case in.DiffStatus == "no-changes":
		l.emit("No changes detected in rendered Kubernetes manifests.")
	case in.DiffStatus == "changes":
		l.emit("Changes detected in **"+itoa(in.DiffTotal)+"** resource(s) across **"+itoa(in.DiffReleases)+"** release(s).",
			"Please review before merging.",
			"")
		if in.DiffTruncated && in.RunURL != "" {
			l.emit("> [!NOTE]",
				"> Some diffs were truncated to fit the PR comment size limit. See the [job summary]("+in.RunURL+") for the full output.",
				"")
		}
		l.emit("### Affected releases", "")
		if in.DiffBlocks != "" {
			// diff.sh wrote the blocks with echo (one newline more), comment.sh
			// inlined them through $(cat …) (trailing newlines stripped)
			l.b.WriteString(strings.TrimRight(in.DiffBlocks, "\n") + "\n")
		}
	case in.DiffStatus == "empty":
		l.emit("No releases were rendered on either side. Check your helmfile configuration if this is unexpected.")
	}

	l.emit("", "---")
	if in.RunURL != "" {
		l.emit("*Generated by [ATLAS](https://github.com/max06/atlas) Review — [Re-run](" + in.RunURL + ")*")
	} else {
		l.emit("*Generated by [ATLAS](https://github.com/max06/atlas) Review*")
	}
	return l.b.String()
}

// pinMessages states how the caller pins the review workflow:
//
//	@main                    → silent (always gets security fixes)
//	@SHA matching main HEAD  → silent (equivalent to @main)
//	pin changed by PR        → neutral note (regardless of old/new version)
//	@SHA not matching main   → warning (pinned to unknown commit)
//	@latest-tag              → note: on latest, but @main recommended for auto-fixes
//	@old-tag                 → warning: behind latest, may miss security mitigations
//	not detected             → silent (no false alarm on renamed/forked workflows)
func pinMessages(l *lines, in CommentInput) {
	merge, target := in.WorkflowPinMerge, in.WorkflowPinTarget
	if merge == "" || merge == "main" {
		return
	}
	isSHA := shaPattern.MatchString(merge)
	if isSHA && in.LatestMainSHA != "" && merge == in.LatestMainSHA {
		return
	}
	changed := target != "" && target != merge
	if changed {
		l.emit("> [!NOTE]", "> This PR changes the ATLAS workflow pin: `@"+target+"` → `@"+merge+"`.", "")
	}
	switch {
	case isSHA:
		if !changed {
			l.emit("> [!WARNING]",
				"> Workflow pinned to a commit SHA (`"+merge[:7]+"`) that does not match the current `main` branch. This pin may miss secret-leak mitigations added in newer commits.",
				"")
		}
	case in.LatestTag != "":
		if merge == in.LatestTag {
			l.emit("> [!NOTE]",
				"> Workflow pinned to the latest release (`@"+merge+"`). Using `@main` is recommended to receive secret-leak mitigations automatically.",
				"")
		} else if !changed {
			l.emit("> [!WARNING]",
				"> Workflow pinned to `@"+merge+"`, latest is `@"+in.LatestTag+"`. Older versions may miss secret-leak mitigations added in newer releases.",
				"")
		}
	}
}

// Summary builds the job summary (complete diff, pin table).
func Summary(in CommentInput) string {
	var l lines
	cell := func(s string) string {
		if s == "" {
			return "—"
		}
		return "`" + s + "`"
	}
	l.emit("## ATLAS Review Summary", "",
		"### Workflow pin detection", "",
		"| Source | Pin |",
		"| --- | --- |",
		"| Target branch | "+cell(in.WorkflowPinTarget)+" |",
		"| Merge result | "+cell(in.WorkflowPinMerge)+" |",
		"| Upstream latest release | "+cell(in.LatestTag)+" |",
		"")
	if strings.TrimSpace(in.SummaryBlocks) != "" {
		l.emit("### Manifest diff", "")
		l.b.WriteString(in.SummaryBlocks)
	}
	return l.b.String()
}

func itoa(n int) string { return strconv.Itoa(n) }
