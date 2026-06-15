package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/robertolupi/botfam/internal/forge"
	"github.com/spf13/cobra"
)

// ExtractOptions holds flags for the session extract command.
type ExtractOptions struct {
	Milestone         string // title or ID
	Out               string // file path
	Since             string // ISO-8601
	Until             string // ISO-8601
	Redact            bool
	InteractionOnly   bool
	WithDiffs         bool
	SnapshotTimestamp string
}

// sessionExtract is the thin args/io entry point retained for tests; it builds
// the Cobra command and runs it against args.
func sessionExtract(args []string, out io.Writer) error {
	return runCobra(newSessionExtractCmd(), args, out)
}

// newSessionExtractCmd builds the `botfam session extract` Cobra command.
func newSessionExtractCmd() *cobra.Command {
	var opts ExtractOptions
	var noRedact bool
	c := &cobra.Command{
		Use:           "extract --milestone <title-or-id>",
		Short:         "Extract a milestone's chronological session timeline for review",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Redact = opts.Redact && !noRedact
			return extractSession(opts, cmd.OutOrStdout())
		},
	}
	c.Flags().StringVar(&opts.Milestone, "milestone", "", "milestone title or numeric ID (required)")
	c.Flags().StringVar(&opts.Out, "out", "", "output file path (default: stdout)")
	c.Flags().StringVar(&opts.Since, "since", "", "only include events at/after this RFC3339 timestamp")
	c.Flags().StringVar(&opts.Until, "until", "", "only include events at/before this RFC3339 timestamp")
	c.Flags().StringVar(&opts.SnapshotTimestamp, "snapshot-timestamp", "", "freeze the timeline at this RFC3339 timestamp for reproducibility")
	c.Flags().BoolVar(&opts.Redact, "redact", true, "scrub secrets/paths before output")
	c.Flags().BoolVar(&noRedact, "no-redact", false, "disable redaction (trusts the input)")
	c.Flags().BoolVar(&opts.InteractionOnly, "interaction-only", false, "omit the technical diff summary")
	c.Flags().BoolVar(&opts.WithDiffs, "with-diffs", false, "append full raw diffs instead of a summary")
	return c
}

func extractSession(opts ExtractOptions, out io.Writer) error {
	if opts.Milestone == "" {
		return errors.New("missing required flag: --milestone <title-or-id>")
	}

	var sinceTime, untilTime, snapshotTime time.Time
	if opts.Since != "" {
		t, err := time.Parse(time.RFC3339, opts.Since)
		if err != nil {
			return fmt.Errorf("invalid --since format (expected RFC3339, e.g. 2006-01-02T15:04:05Z): %w", err)
		}
		sinceTime = t
	}
	if opts.Until != "" {
		t, err := time.Parse(time.RFC3339, opts.Until)
		if err != nil {
			return fmt.Errorf("invalid --until format (expected RFC3339, e.g. 2006-01-02T15:04:05Z): %w", err)
		}
		untilTime = t
	}
	if opts.SnapshotTimestamp != "" {
		t, err := time.Parse(time.RFC3339, opts.SnapshotTimestamp)
		if err != nil {
			return fmt.Errorf("invalid --snapshot-timestamp format (expected RFC3339, e.g. 2006-01-02T15:04:05Z): %w", err)
		}
		snapshotTime = t
	}

	var actor string
	if info, err := (GitResolver{}).ResolveIdentity("."); err == nil {
		actor = info.Actor
	}

	client, err := forge.NewClient(".", actor)
	if err != nil {
		return err
	}

	// 1. Resolve Milestone
	milestones, err := client.ListMilestones()
	if err != nil {
		return fmt.Errorf("failed to list milestones: %w", err)
	}

	var matched *forge.Milestone
	var matches []*forge.Milestone

	// Try ID matching first if it looks like an integer
	if id, err := strconv.ParseInt(opts.Milestone, 10, 64); err == nil {
		for _, m := range milestones {
			if m.ID == id {
				matched = m
				break
			}
		}
	}

	// Try Title matching
	if matched == nil {
		for _, m := range milestones {
			if strings.EqualFold(m.Title, opts.Milestone) {
				matches = append(matches, m)
			}
		}

		if len(matches) == 1 {
			matched = matches[0]
		} else if len(matches) > 1 {
			// Prioritize open milestones
			var openMatches []*forge.Milestone
			for _, m := range matches {
				if m.State == "open" {
					openMatches = append(openMatches, m)
				}
			}
			if len(openMatches) == 1 {
				matched = openMatches[0]
			} else {
				// Ambiguity unresolved
				var ids []string
				for _, m := range matches {
					ids = append(ids, fmt.Sprintf("%d (%s)", m.ID, m.State))
				}
				return fmt.Errorf("milestone title %q is ambiguous; matching IDs: %s. Please specify the unique milestone ID", opts.Milestone, strings.Join(ids, ", "))
			}
		}
	}

	if matched == nil {
		return fmt.Errorf("milestone %q not found", opts.Milestone)
	}

	// 2. Fetch issues/PRs in milestone
	issues, err := client.ListIssuesByMilestone(matched.ID)
	if err != nil {
		return fmt.Errorf("failed to fetch issues: %w", err)
	}

	// 3. Extract events
	var events []*timelineEntry
	var prs []*forge.Issue

	earliestEvent := time.Now()
	latestEvent := time.Time{}

	for _, issue := range issues {
		isPR := issue.PullRequest != nil
		tag := fmt.Sprintf("[Issue #%d]", issue.Number)
		if isPR {
			tag = fmt.Sprintf("[PR #%d]", issue.Number)
			prs = append(prs, issue)
		}

		// Initial opened event
		openedTime, parseErr := time.Parse(time.RFC3339, issue.CreatedAt)
		if parseErr == nil {
			if (opts.Since == "" || !openedTime.Before(sinceTime)) &&
				(opts.Until == "" || !openedTime.After(untilTime)) &&
				(opts.SnapshotTimestamp == "" || !openedTime.After(snapshotTime)) {

				events = append(events, &timelineEntry{
					Timestamp: openedTime,
					Tag:       tag,
					Actor:     issue.User.Login,
					Action:    fmt.Sprintf("opened: %s", issue.Title),
					Body:      issue.Body,
					EventID:   0, // sort synthetic opened event first
				})

				if openedTime.Before(earliestEvent) {
					earliestEvent = openedTime
				}
				if openedTime.After(latestEvent) {
					latestEvent = openedTime
				}
			}
		}

		// Gitea timeline events
		rawTimeline, err := client.GetIssueTimeline(issue.Number)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to fetch timeline for issue #%d: %v\n", issue.Number, err)
			continue
		}

		for _, e := range rawTimeline {
			eventTime, parseErr := time.Parse(time.RFC3339, e.CreatedAt)
			if parseErr != nil {
				continue
			}

			// Apply filters
			if opts.Since != "" && eventTime.Before(sinceTime) {
				continue
			}
			if opts.Until != "" && eventTime.After(untilTime) {
				continue
			}
			if opts.SnapshotTimestamp != "" && eventTime.After(snapshotTime) {
				continue
			}

			if eventTime.Before(earliestEvent) {
				earliestEvent = eventTime
			}
			if eventTime.After(latestEvent) {
				latestEvent = eventTime
			}

			// Format events
			actor := "system"
			if e.User != nil {
				actor = e.User.Login
			}

			action := ""
			body := e.Body

			switch e.Type {
			case "comment":
				action = "commented:"
			case "review":
				// Attempt to get state, body from comments / reviews structure if possible
				action = "submitted review:"
			case "pull_push":
				action = "pushed commits"
				if e.Body != "" {
					var pushData struct {
						CommitIDs   []string `json:"commit_ids"`
						IsForcePush bool     `json:"is_force_push"`
					}
					if err := json.Unmarshal([]byte(e.Body), &pushData); err == nil {
						force := ""
						if pushData.IsForcePush {
							force = " force-pushed"
						}
						shas := []string{}
						for _, sha := range pushData.CommitIDs {
							if len(sha) > 8 {
								shas = append(shas, sha[:8])
							} else {
								shas = append(shas, sha)
							}
						}
						action = fmt.Sprintf("pushed%s commits: %s", force, strings.Join(shas, ", "))
						body = ""
					}
				}
			case "merge_pull":
				action = "merged pull request"
				if e.RefCommitSHA != "" {
					sha := e.RefCommitSHA
					if len(sha) > 8 {
						sha = sha[:8]
					}
					action += fmt.Sprintf(" (commit %s)", sha)
				}
			case "closed":
				action = "closed issue/PR"
			case "reopened":
				action = "reopened issue/PR"
			case "assigned":
				action = "assigned"
			default:
				action = fmt.Sprintf("event %s", e.Type)
			}

			events = append(events, &timelineEntry{
				Timestamp: eventTime,
				Tag:       tag,
				Actor:     actor,
				Action:    action,
				Body:      body,
				EventID:   e.ID,
			})
		}
	}

	// 4. Stable Sort
	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].Timestamp.Equal(events[j].Timestamp) {
			return events[i].Timestamp.Before(events[j].Timestamp)
		}
		if events[i].EventID != events[j].EventID {
			return events[i].EventID < events[j].EventID
		}
		if events[i].Tag != events[j].Tag {
			return events[i].Tag < events[j].Tag
		}
		return events[i].Actor < events[j].Actor
	})

	// 5. De-duplication of events
	events = deduplicateEvents(events)

	// 6. Build markdown output
	var buf strings.Builder
	fmt.Fprintf(&buf, "# Session/Milestone: %s\n\n", matched.Title)

	// Milestone Hygiene completeness warning
	if len(events) > 0 {
		warnings := checkMilestoneHygiene(client, earliestEvent, latestEvent, issues)
		for _, w := range warnings {
			fmt.Fprintf(&buf, "> [!WARNING]\n> %s\n\n", w)
		}
	}

	fmt.Fprintln(&buf, "## Timeline")
	for _, ev := range events {
		fmt.Fprintf(&buf, "- **%s** - %s `%s` %s", ev.Timestamp.Format(time.RFC3339), ev.Tag, ev.Actor, ev.Action)
		trimmedBody := strings.TrimSpace(ev.Body)
		if trimmedBody != "" {
			fmt.Fprintln(&buf)
			// Blockquote formatting for message body
			lines := strings.Split(trimmedBody, "\n")
			for _, line := range lines {
				fmt.Fprintf(&buf, "  > %s\n", line)
			}
		} else {
			fmt.Fprintln(&buf)
		}
	}

	// 7. Append Technical Diff summaries
	if !opts.InteractionOnly && len(prs) > 0 {
		fmt.Fprintln(&buf, "\n---")
		fmt.Fprintln(&buf, "\n## Technical Diff Summary")

		for _, pr := range prs {
			fmt.Fprintf(&buf, "\n### Files Changed in PR #%d:\n", pr.Number)
			diffText, diffErr := client.GetPRDiff(pr.Number)
			if diffErr != nil {
				fmt.Fprintf(&buf, "_(Error fetching PR diff: %v)_\n", diffErr)
				continue
			}

			if opts.WithDiffs {
				fmt.Fprintf(&buf, "```diff\n%s\n```\n", diffText)
			} else {
				summary := parseDiffSummary(diffText)
				if summary == "" {
					fmt.Fprintln(&buf, "_(No code diff or empty changes)_")
				} else {
					fmt.Fprint(&buf, summary)
				}
			}
		}
	}

	output := buf.String()

	// 8. Redaction Filter
	if opts.Redact {
		output = redactSecrets(output)
	}

	// Character-based context budget check warning
	tokenApprox := len(output) / 4
	if tokenApprox > 50000 {
		warnMsg := fmt.Sprintf("WARNING: Extracted session is very large (~%d tokens). Consider using temporal bounds (--since/--until) or --interaction-only.", tokenApprox)
		fmt.Fprintln(os.Stderr, warnMsg)
		output = fmt.Sprintf("> [!IMPORTANT]\n> %s\n\n%s", warnMsg, output)
	}

	// 9. Write output
	if opts.Out != "" {
		if err := os.WriteFile(opts.Out, []byte(output), 0o644); err != nil {
			return fmt.Errorf("failed to write output file: %w", err)
		}
		fmt.Fprintf(out, "Extracted session for milestone %q to %s\n", matched.Title, opts.Out)
	} else {
		fmt.Fprint(out, output)
	}

	return nil
}

type timelineEntry struct {
	Timestamp time.Time
	Tag       string
	Actor     string
	Action    string
	Body      string
	EventID   int64
}

func deduplicateEvents(events []*timelineEntry) []*timelineEntry {
	if len(events) == 0 {
		return events
	}
	var deduped []*timelineEntry
	seen := make(map[string]bool)
	for _, e := range events {
		// Create a key based on timestamp, tag, actor, action, and body content
		key := fmt.Sprintf("%s|%s|%s|%s|%s", e.Timestamp.Format(time.RFC3339), e.Tag, e.Actor, e.Action, e.Body)
		if !seen[key] {
			seen[key] = true
			deduped = append(deduped, e)
		}
	}
	return deduped
}

func redactSecrets(input string) string {
	// 1. Redact Authorization tokens
	authReg := regexp.MustCompile(`(?i)(token\s+|Authorization:\s*token\s+)[a-zA-Z0-9_-]{40}`)
	input = authReg.ReplaceAllStringFunc(input, func(m string) string {
		parts := strings.Split(m, " ")
		if len(parts) > 1 {
			return parts[0] + " [REDACTED_TOKEN]"
		}
		return "[REDACTED_TOKEN]"
	})

	// 2. Redact general API keys / credentials
	credReg := regexp.MustCompile(`(?i)(api[-_]?key|password|secret|token)\s*[:=]\s*["']?[a-zA-Z0-9_-]{16,}["']?`)
	input = credReg.ReplaceAllStringFunc(input, func(m string) string {
		parts := strings.Split(m, ":")
		if len(parts) == 2 {
			return parts[0] + ": [REDACTED_CREDENTIAL]"
		}
		parts = strings.Split(m, "=")
		if len(parts) == 2 {
			return parts[0] + "= [REDACTED_CREDENTIAL]"
		}
		return "[REDACTED_CREDENTIAL]"
	})

	// 3. Redact local path structures (e.g. /Users/rlupi/...)
	pathReg := regexp.MustCompile(`/(Users|home)/[a-zA-Z0-9_-]+/[a-zA-Z0-9_.-]+(/[a-zA-Z0-9_.-]+)*`)
	input = pathReg.ReplaceAllString(input, "[REDACTED_PATH]")

	return input
}

func checkMilestoneHygiene(c *forge.Client, start, end time.Time, milestoneIssues []*forge.Issue) []string {
	var warnings []string
	if start.IsZero() || end.IsZero() {
		return warnings
	}

	// Query last 50 issues/PRs from repository
	path := fmt.Sprintf("repos/%s/%s/issues?state=all&page=1&limit=50", c.Owner, c.Repo)
	b, err := c.Request("GET", path, nil)
	if err != nil {
		return warnings
	}
	var recent []*forge.Issue
	if err := json.Unmarshal(b, &recent); err != nil {
		return warnings
	}

	linked := make(map[int]bool)
	for _, issue := range milestoneIssues {
		linked[issue.Number] = true
	}

	for _, issue := range recent {
		if linked[issue.Number] {
			continue
		}
		// Parse updated_at / closed_at to see if they fall into the milestone active range
		itemTime, err := time.Parse(time.RFC3339, issue.CreatedAt)
		if err != nil {
			continue
		}
		if itemTime.After(start) && itemTime.Before(end) {
			isPR := issue.PullRequest != nil
			itemType := "Issue"
			if isPR {
				itemType = "PR"
			}
			warnings = append(warnings, fmt.Sprintf("%s #%d (%s) was created during the milestone timeframe (%s) but is not linked to this milestone.", itemType, issue.Number, issue.Title, itemTime.Format(time.RFC3339)))
		}
	}

	return warnings
}

func parseDiffSummary(diffText string) string {
	lines := strings.Split(diffText, "\n")
	var fileSummary []string
	var currentFile string
	var currentAdds, currentDels int
	var funcsTouched []string

	flush := func() {
		if currentFile != "" {
			funcStr := ""
			if len(funcsTouched) > 0 {
				funcStr = fmt.Sprintf("\n    - Touched: %s", strings.Join(funcsTouched, ", "))
			}
			fileSummary = append(fileSummary, fmt.Sprintf("- **%s** (+%d, -%d)%s\n", currentFile, currentAdds, currentDels, funcStr))
			funcsTouched = nil
		}
	}

	funcReg := regexp.MustCompile(`@@\s+-\d+(?:,\d+)?\s+\+\d+(?:,\d+)?\s+@@\s*(.*)`)

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git a/") {
			flush()
			currentAdds = 0
			currentDels = 0
			parts := strings.Split(line, " ")
			if len(parts) >= 4 {
				// e.g. b/internal/fam/newfam.go
				currentFile = strings.TrimPrefix(parts[3], "b/")
			} else {
				currentFile = "unknown"
			}
		} else if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			currentAdds++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			currentDels++
		} else if strings.HasPrefix(line, "@@") {
			match := funcReg.FindStringSubmatch(line)
			if len(match) > 1 && strings.TrimSpace(match[1]) != "" {
				fn := strings.TrimSpace(match[1])
				// Clean up function signature if it has braces or extra info
				if idx := strings.Index(fn, "{"); idx != -1 {
					fn = strings.TrimSpace(fn[:idx])
				}
				// Deduplicate
				seen := false
				for _, f := range funcsTouched {
					if f == "`"+fn+"`" {
						seen = true
						break
					}
				}
				if !seen {
					funcsTouched = append(funcsTouched, "`"+fn+"`")
				}
			}
		}
	}
	flush()

	return strings.Join(fileSummary, "")
}
