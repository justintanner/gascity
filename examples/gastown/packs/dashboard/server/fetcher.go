package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// runCmd executes a command with a timeout and returns stdout.
// Returns empty buffer on timeout or error.
// Security: errors from this function are logged server-side only (via log.Printf
// in callers) and never included in HTTP responses. The handler renders templates
// with whatever data was successfully fetched; fetch failures result in empty panels.
func runCmd(timeout time.Duration, name string, args ...string) (*bytes.Buffer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("%s timed out after %v", name, timeout)
		}
		return nil, err
	}
	return &stdout, nil
}

var fetcherRunCmd = runCmd

// runBdCmd executes a bd command with the configured cmdTimeout in the specified beads directory.
func (f *LiveConvoyFetcher) runBdCmd(rigPath string, args ...string) (*bytes.Buffer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), f.cmdTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bd", args...)
	cmd.Dir = rigPath
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	err := cmd.Run()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("bd timed out after %v", f.cmdTimeout)
		}
		// If we got some output, return it anyway (bd may exit non-zero with warnings)
		if stdout.Len() > 0 {
			return &stdout, nil
		}
		return nil, err
	}
	return &stdout, nil
}

// runGcCmd executes a gc command with the configured cmdTimeout in the city context.
func (f *LiveConvoyFetcher) runGcCmd(args ...string) (*bytes.Buffer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), f.cmdTimeout)
	defer cancel()

	// Prepend --city flag so gc knows which city to operate on.
	fullArgs := append([]string{"--city", f.cityPath}, args...)
	cmd := exec.CommandContext(ctx, "gc", fullArgs...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	err := cmd.Run()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("gc timed out after %v", f.cmdTimeout)
		}
		if stdout.Len() > 0 {
			return &stdout, nil
		}
		return nil, err
	}
	return &stdout, nil
}

// Activity color constants (inlined from upstream internal/activity).
const (
	colorGreen   = "green"
	colorYellow  = "yellow"
	colorRed     = "red"
	colorUnknown = "unknown"
)

// Activity thresholds for color coding.
const (
	thresholdActive = 5 * time.Minute  // Green threshold
	thresholdStale  = 10 * time.Minute // Yellow threshold (beyond this is red)
)

// Default GUPP violation timeout (30 min, same as upstream).
const defaultGUPPViolationTimeout = 30 * time.Minute

// calculateActivity computes activity info from a last-activity timestamp.
// Replaces upstream activity.Calculate.
func calculateActivity(lastActivity time.Time) ActivityInfo {
	if lastActivity.IsZero() {
		return ActivityInfo{
			Display:    "unknown",
			ColorClass: colorUnknown,
		}
	}

	d := time.Since(lastActivity)
	if d < 0 {
		d = 0
	}

	return ActivityInfo{
		Display:    formatAge(d),
		ColorClass: colorForDuration(d),
	}
}

// formatAge formats a duration as a short human-readable string.
func formatAge(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// colorForDuration returns the color class for a given duration.
func colorForDuration(d time.Duration) string {
	switch {
	case d < thresholdActive:
		return colorGreen
	case d < thresholdStale:
		return colorYellow
	default:
		return colorRed
	}
}

// extractIssueID unwraps "external:prefix:id" to just "id".
// Replaces upstream beads.ExtractIssueID.
func extractIssueID(id string) string {
	if strings.HasPrefix(id, "external:") {
		parts := strings.SplitN(id, ":", 3)
		if len(parts) == 3 {
			return parts[2]
		}
	}
	return id
}

// LiveConvoyFetcher fetches convoy data from beads using gc and bd CLIs.
type LiveConvoyFetcher struct {
	cityPath string
	cityName string

	// Configurable timeouts
	cmdTimeout     time.Duration
	ghCmdTimeout   time.Duration
	tmuxCmdTimeout time.Duration

	// Configurable worker status thresholds
	staleThreshold          time.Duration
	stuckThreshold          time.Duration
	heartbeatFreshThreshold time.Duration
	mayorActiveThreshold    time.Duration
}

// NewLiveConvoyFetcher creates a fetcher for the given city.
func NewLiveConvoyFetcher(cityPath string, cityName string) *LiveConvoyFetcher {
	return &LiveConvoyFetcher{
		cityPath:                cityPath,
		cityName:                cityName,
		cmdTimeout:              15 * time.Second,
		ghCmdTimeout:            10 * time.Second,
		tmuxCmdTimeout:          2 * time.Second,
		staleThreshold:          5 * time.Minute,
		stuckThreshold:          defaultGUPPViolationTimeout,
		heartbeatFreshThreshold: 5 * time.Minute,
		mayorActiveThreshold:    5 * time.Minute,
	}
}

// statusJSON is the shape returned by `gc status --json`.
type statusJSON struct {
	City string `json:"city"`
	Rigs []struct {
		Name      string `json:"name"`
		Path      string `json:"path"`
		Suspended bool   `json:"suspended"`
	} `json:"rigs"`
}

// agentListEntry is the shape returned by `gc agent list --json`.
type agentListEntry struct {
	Name    string `json:"name"`
	Role    string `json:"role"`
	Pool    string `json:"pool"`
	Session string `json:"session"`
	Status  string `json:"status"`
	Rig     string `json:"rig"`
	Runtime string `json:"runtime"`
}

// FetchRigs returns all registered rigs using gc status --json.
func (f *LiveConvoyFetcher) FetchRigs() ([]RigRow, error) {
	stdout, err := f.runGcCmd("status", "--json")
	if err != nil {
		return nil, fmt.Errorf("gc status --json: %w", err)
	}

	var status statusJSON
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		return nil, fmt.Errorf("parsing gc status: %w", err)
	}

	rows := make([]RigRow, 0, len(status.Rigs))
	for _, rig := range status.Rigs {
		row := RigRow{
			Name: rig.Name,
		}
		// Additional counts (polecats, crew, witness, refinery) would require
		// deeper inspection. For now, populate the name and mark unsuspended
		// rigs as available.
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Name < rows[j].Name
	})

	return rows, nil
}

// FetchMayor returns the coordinator agent's status using gc agent list --json.
func (f *LiveConvoyFetcher) FetchMayor() (*MayorStatus, error) {
	status := &MayorStatus{
		IsAttached: false,
	}

	agents, err := f.fetchAgentList()
	if err != nil {
		return status, nil
	}

	// Find the agent named "mayor" or the first city-scoped agent.
	var mayor *agentListEntry
	for i := range agents {
		if agents[i].Name == "mayor" {
			mayor = &agents[i]
			break
		}
	}
	if mayor == nil && len(agents) > 0 {
		// Fall back to first agent with no pool (city-scoped).
		for i := range agents {
			if agents[i].Pool == "" {
				mayor = &agents[i]
				break
			}
		}
	}

	if mayor == nil {
		return status, nil
	}

	status.SessionName = mayor.Session
	if status.SessionName == "" {
		status.SessionName = mayor.Name
	}
	status.Runtime = mayor.Runtime

	// Check tmux session for activity.
	stdout, err := runCmd(f.tmuxCmdTimeout, "tmux", "list-sessions", "-F", "#{session_name}:#{session_activity}")
	if err != nil {
		return status, nil
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, status.SessionName+":") {
			status.IsAttached = true
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				if activityTs, ok := parseActivityTimestamp(parts[1]); ok {
					age := time.Since(time.Unix(activityTs, 0))
					status.LastActivity = formatTimestamp(time.Unix(activityTs, 0))
					status.IsActive = age < f.mayorActiveThreshold
				}
			}
			break
		}
	}

	if status.IsAttached && status.Runtime == "" {
		status.Runtime = "claude"
	}

	return status, nil
}

// FetchDogs returns city-scoped pool agents (rig == "").
func (f *LiveConvoyFetcher) FetchDogs() ([]DogRow, error) {
	agents, err := f.fetchAgentList()
	if err != nil {
		return nil, nil
	}

	var rows []DogRow
	for _, agent := range agents {
		if agent.Rig != "" || agent.Pool == "" {
			continue
		}

		state := "idle"
		if agent.Status == "working" || agent.Status == "hooked" {
			state = "working"
		}

		row := DogRow{
			Name:  agent.Name,
			State: state,
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Name < rows[j].Name
	})

	return rows, nil
}

// fetchAgentList calls gc agent list --json and returns the parsed entries.
func (f *LiveConvoyFetcher) fetchAgentList() ([]agentListEntry, error) {
	stdout, err := f.runGcCmd("agent", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("gc agent list --json: %w", err)
	}

	var agents []agentListEntry
	if err := json.Unmarshal(stdout.Bytes(), &agents); err != nil {
		return nil, fmt.Errorf("parsing agent list: %w", err)
	}

	return agents, nil
}

// FetchSessions returns active tmux sessions.
func (f *LiveConvoyFetcher) FetchSessions() ([]SessionRow, error) {
	stdout, err := runCmd(f.tmuxCmdTimeout, "tmux", "list-sessions", "-F", "#{session_name}:#{session_activity}")
	if err != nil {
		return nil, nil // tmux not running or no sessions
	}

	var rows []SessionRow
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		name := parts[0]

		row := SessionRow{
			Name:    name,
			IsAlive: true,
		}

		// Parse activity timestamp
		if len(parts) > 1 {
			if ts, ok := parseActivityTimestamp(parts[1]); ok && ts > 0 {
				row.Activity = formatTimestamp(time.Unix(ts, 0))
			}
		}

		// In Gas City, session names default to just the agent name.
		// Try to extract role/rig info from naming conventions.
		row.Role, row.Rig, row.Worker = parseSessionParts(name)

		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Rig != rows[j].Rig {
			return rows[i].Rig < rows[j].Rig
		}
		if rows[i].Role != rows[j].Role {
			return rows[i].Role < rows[j].Role
		}
		return rows[i].Worker < rows[j].Worker
	})

	return rows, nil
}

// parseSessionParts extracts role, rig, and worker from a session name.
// Gas City sessions default to the agent name. For gastown-style sessions
// like "rig-role-worker", we split on hyphens.
func parseSessionParts(name string) (role, rig, worker string) {
	parts := strings.SplitN(name, "-", 3)
	switch len(parts) {
	case 3:
		// "rig-role-worker" style
		rig = parts[0]
		role = parts[1]
		worker = parts[2]
	case 2:
		// "role-worker" or "rig-role"
		role = parts[0]
		worker = parts[1]
	default:
		// Just the agent name
		role = name
		worker = name
	}
	return
}

// FetchConvoys fetches all open convoys with their activity data.
func (f *LiveConvoyFetcher) FetchConvoys() ([]ConvoyRow, error) {
	stdout, err := f.runBdCmd(f.cityPath, "list", "--type=convoy", "--status=open", "--json")
	if err != nil {
		return nil, fmt.Errorf("listing convoys: %w", err)
	}

	var convoys []struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Status    string `json:"status"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &convoys); err != nil {
		return nil, fmt.Errorf("parsing convoy list: %w", err)
	}

	rows := make([]ConvoyRow, 0, len(convoys))
	for _, c := range convoys {
		row := ConvoyRow{
			ID:     c.ID,
			Title:  c.Title,
			Status: c.Status,
		}

		// Get tracked issues for progress and activity calculation.
		tracked, err := f.getTrackedIssues(c.ID)
		if err != nil {
			log.Printf("warning: skipping convoy %s: %v", c.ID, err)
			continue
		}
		row.Total = len(tracked)

		var mostRecentActivity time.Time
		var mostRecentUpdated time.Time
		var hasAssignee bool
		for _, t := range tracked {
			if t.Status == "closed" {
				row.Completed++
			}
			if t.LastActivity.After(mostRecentActivity) {
				mostRecentActivity = t.LastActivity
			}
			if t.UpdatedAt.After(mostRecentUpdated) {
				mostRecentUpdated = t.UpdatedAt
			}
			if t.Assignee != "" {
				hasAssignee = true
			}
		}

		row.Progress = fmt.Sprintf("%d/%d", row.Completed, row.Total)

		// Calculate activity info from most recent worker activity.
		if !mostRecentActivity.IsZero() {
			row.LastActivity = calculateActivity(mostRecentActivity)
		} else if !hasAssignee {
			if polecatActivity := f.getAllWorkerActivity(); polecatActivity != nil {
				info := calculateActivity(*polecatActivity)
				info.Display = info.Display + " (worker active)"
				row.LastActivity = info
			} else if !mostRecentUpdated.IsZero() {
				info := calculateActivity(mostRecentUpdated)
				info.Display = info.Display + " (unassigned)"
				row.LastActivity = info
			} else {
				row.LastActivity = ActivityInfo{
					Display:    "unassigned",
					ColorClass: colorUnknown,
				}
			}
		} else {
			row.LastActivity = ActivityInfo{
				Display:    "idle",
				ColorClass: colorUnknown,
			}
		}

		// Calculate work status based on progress and activity.
		row.WorkStatus = calculateWorkStatus(row.Completed, row.Total, row.LastActivity.ColorClass)

		// Get tracked issues for expandable view.
		row.TrackedIssues = make([]TrackedIssue, len(tracked))
		for i, t := range tracked {
			row.TrackedIssues[i] = TrackedIssue{
				ID:       t.ID,
				Title:    t.Title,
				Status:   t.Status,
				Assignee: t.Assignee,
			}
		}

		rows = append(rows, row)
	}

	return rows, nil
}

// trackedIssueInfo holds info about an issue being tracked by a convoy.
type trackedIssueInfo struct {
	ID           string
	Title        string
	Status       string
	Assignee     string
	LastActivity time.Time
	UpdatedAt    time.Time
}

// getTrackedIssues fetches tracked issues for a convoy.
func (f *LiveConvoyFetcher) getTrackedIssues(convoyID string) ([]trackedIssueInfo, error) {
	stdout, err := f.runBdCmd(f.cityPath, "dep", "list", convoyID, "-t", "tracks", "--json")
	if err != nil {
		return nil, fmt.Errorf("querying tracked issues for %s: %w", convoyID, err)
	}

	var deps []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &deps); err != nil {
		return nil, fmt.Errorf("parsing tracked issues for %s: %w", convoyID, err)
	}

	issueIDs := make([]string, 0, len(deps))
	for _, dep := range deps {
		issueIDs = append(issueIDs, extractIssueID(dep.ID))
	}

	details, err := f.getIssueDetailsBatch(issueIDs)
	if err != nil {
		return nil, fmt.Errorf("fetching tracked issue details for %s: %w", convoyID, err)
	}

	workers := f.getWorkersFromAssignees(details)

	result := make([]trackedIssueInfo, 0, len(issueIDs))
	for _, id := range issueIDs {
		info := trackedIssueInfo{ID: id}

		if d, ok := details[id]; ok {
			info.Title = d.Title
			info.Status = d.Status
			info.Assignee = d.Assignee
			info.UpdatedAt = d.UpdatedAt
		} else {
			info.Title = "(external)"
			info.Status = "unknown"
		}

		if w, ok := workers[id]; ok && w.LastActivity != nil {
			info.LastActivity = *w.LastActivity
		}

		result = append(result, info)
	}

	return result, nil
}

// issueDetail holds basic issue info.
type issueDetail struct {
	ID        string
	Title     string
	Status    string
	Assignee  string
	UpdatedAt time.Time
}

// getIssueDetailsBatch fetches details for multiple issues.
func (f *LiveConvoyFetcher) getIssueDetailsBatch(issueIDs []string) (map[string]*issueDetail, error) {
	result := make(map[string]*issueDetail)
	if len(issueIDs) == 0 {
		return result, nil
	}

	args := append([]string{"show"}, issueIDs...)
	args = append(args, "--json")

	stdout, err := fetcherRunCmd(f.cmdTimeout, "bd", args...)
	if err != nil {
		return nil, fmt.Errorf("bd show failed (issue_count=%d): %w", len(issueIDs), err)
	}

	var issues []struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Status    string `json:"status"`
		Assignee  string `json:"assignee"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &issues); err != nil {
		return nil, fmt.Errorf("bd show returned invalid JSON (issue_count=%d): %w", len(issueIDs), err)
	}

	for _, issue := range issues {
		detail := &issueDetail{
			ID:       issue.ID,
			Title:    issue.Title,
			Status:   issue.Status,
			Assignee: issue.Assignee,
		}
		if issue.UpdatedAt != "" {
			if t, err := time.Parse(time.RFC3339, issue.UpdatedAt); err == nil {
				detail.UpdatedAt = t
			}
		}
		result[issue.ID] = detail
	}

	return result, nil
}

// workerDetail holds worker info including last activity.
type workerDetail struct {
	Worker       string
	LastActivity *time.Time
}

// getWorkersFromAssignees gets worker activity from tmux sessions based on issue assignees.
func (f *LiveConvoyFetcher) getWorkersFromAssignees(details map[string]*issueDetail) map[string]*workerDetail {
	result := make(map[string]*workerDetail)

	assigneeToIssues := make(map[string][]string)
	for issueID, detail := range details {
		if detail == nil || detail.Assignee == "" {
			continue
		}
		assigneeToIssues[detail.Assignee] = append(assigneeToIssues[detail.Assignee], issueID)
	}

	if len(assigneeToIssues) == 0 {
		return result
	}

	for assignee, issueIDs := range assigneeToIssues {
		activity := f.getSessionActivityForAssignee(assignee)
		if activity == nil {
			continue
		}
		for _, issueID := range issueIDs {
			result[issueID] = &workerDetail{
				Worker:       assignee,
				LastActivity: activity,
			}
		}
	}

	return result
}

// getSessionActivityForAssignee looks up tmux session activity for an assignee.
// In Gas City, the assignee may be a simple agent name or a "rig/pool/worker" path.
// The session name defaults to the agent name (no city prefix) unless a session
// template is configured.
func (f *LiveConvoyFetcher) getSessionActivityForAssignee(assignee string) *time.Time {
	// Derive session name from assignee.
	// Gas City assignees can be:
	//   - Simple: "polecat-1" -> session "polecat-1"
	//   - Path: "myrig/polecats/dag" -> try session "dag" then "myrig-dag"
	var sessionName string
	parts := strings.Split(assignee, "/")
	if len(parts) >= 3 {
		// Path format: try the worker name directly first.
		sessionName = parts[2]
	} else {
		sessionName = assignee
	}

	stdout, err := runCmd(f.tmuxCmdTimeout, "tmux", "list-sessions", "-F", "#{session_name}|#{session_activity}",
		"-f", fmt.Sprintf("#{==:#{session_name},%s}", sessionName))
	if err != nil {
		// If simple name didn't work and we have a path, try rig-worker format.
		if len(parts) >= 3 {
			sessionName = parts[0] + "-" + parts[2]
			stdout, err = runCmd(f.tmuxCmdTimeout, "tmux", "list-sessions", "-F", "#{session_name}|#{session_activity}",
				"-f", fmt.Sprintf("#{==:#{session_name},%s}", sessionName))
			if err != nil {
				return nil
			}
		} else {
			return nil
		}
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		return nil
	}

	outputParts := strings.Split(output, "|")
	if len(outputParts) < 2 {
		return nil
	}

	var activityUnix int64
	if _, err := fmt.Sscanf(outputParts[1], "%d", &activityUnix); err != nil || activityUnix == 0 {
		return nil
	}

	t := time.Unix(activityUnix, 0)
	return &t
}

// getAllWorkerActivity returns the most recent activity from any running worker session.
// This is used as a fallback when no specific assignee activity can be determined.
func (f *LiveConvoyFetcher) getAllWorkerActivity() *time.Time {
	stdout, err := runCmd(f.tmuxCmdTimeout, "tmux", "list-sessions", "-F", "#{session_name}|#{session_activity}")
	if err != nil {
		return nil
	}

	var mostRecent time.Time
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Split(line, "|")
		if len(parts) < 2 {
			continue
		}

		var activityUnix int64
		if _, err := fmt.Sscanf(parts[1], "%d", &activityUnix); err != nil || activityUnix == 0 {
			continue
		}

		activityTime := time.Unix(activityUnix, 0)
		if activityTime.After(mostRecent) {
			mostRecent = activityTime
		}
	}

	if mostRecent.IsZero() {
		return nil
	}
	return &mostRecent
}

// calculateWorkStatus determines the work status based on progress and activity.
// Returns: "complete", "active", "stale", "stuck", or "waiting"
func calculateWorkStatus(completed, total int, activityColor string) string {
	if total > 0 && completed == total {
		return "complete"
	}

	switch activityColor {
	case colorGreen:
		return "active"
	case colorYellow:
		return "stale"
	case colorRed:
		return "stuck"
	default:
		return "waiting"
	}
}

// FetchMergeQueue fetches open PRs from registered rigs.
func (f *LiveConvoyFetcher) FetchMergeQueue() ([]MergeQueueRow, error) {
	// Get rigs to find their git URLs.
	stdout, err := f.runGcCmd("status", "--json")
	if err != nil {
		return nil, fmt.Errorf("gc status --json for merge queue: %w", err)
	}

	var status statusJSON
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		return nil, fmt.Errorf("parsing gc status for merge queue: %w", err)
	}

	// For each rig, try to determine repo from its git config.
	var result []MergeQueueRow
	for _, rig := range status.Rigs {
		if rig.Path == "" {
			continue
		}
		repoPath := f.detectRepoFromPath(rig.Path)
		if repoPath == "" {
			continue
		}

		prs, err := f.fetchPRsForRepo(repoPath, rig.Name)
		if err != nil {
			continue
		}
		result = append(result, prs...)
	}

	return result, nil
}

// detectRepoFromPath tries to extract owner/repo from a git working directory.
func (f *LiveConvoyFetcher) detectRepoFromPath(path string) string {
	stdout, err := runCmd(f.ghCmdTimeout, "git", "-C", path, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return gitURLToRepoPath(strings.TrimSpace(stdout.String()))
}

// gitURLToRepoPath converts a git URL to owner/repo format.
func gitURLToRepoPath(gitURL string) string {
	if strings.HasPrefix(gitURL, "https://github.com/") {
		path := strings.TrimPrefix(gitURL, "https://github.com/")
		path = strings.TrimSuffix(path, ".git")
		return path
	}
	if strings.HasPrefix(gitURL, "git@github.com:") {
		path := strings.TrimPrefix(gitURL, "git@github.com:")
		path = strings.TrimSuffix(path, ".git")
		return path
	}
	return ""
}

// prResponse represents the JSON response from gh pr list.
type prResponse struct {
	Number            int    `json:"number"`
	Title             string `json:"title"`
	URL               string `json:"url"`
	Mergeable         string `json:"mergeable"`
	StatusCheckRollup []struct {
		State      string `json:"state"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	} `json:"statusCheckRollup"`
}

// fetchPRsForRepo fetches open PRs for a single repo.
func (f *LiveConvoyFetcher) fetchPRsForRepo(repoFull, repoShort string) ([]MergeQueueRow, error) {
	stdout, err := runCmd(f.ghCmdTimeout, "gh", "pr", "list",
		"--repo", repoFull,
		"--state", "open",
		"--json", "number,title,url,mergeable,statusCheckRollup")
	if err != nil {
		return nil, fmt.Errorf("fetching PRs for %s: %w", repoFull, err)
	}

	var prs []prResponse
	if err := json.Unmarshal(stdout.Bytes(), &prs); err != nil {
		return nil, fmt.Errorf("parsing PRs for %s: %w", repoFull, err)
	}

	result := make([]MergeQueueRow, 0, len(prs))
	for _, pr := range prs {
		row := MergeQueueRow{
			Number: pr.Number,
			Repo:   repoShort,
			Title:  pr.Title,
			URL:    pr.URL,
		}
		row.CIStatus = determineCIStatus(pr.StatusCheckRollup)
		row.Mergeable = determineMergeableStatus(pr.Mergeable)
		row.ColorClass = determineColorClass(row.CIStatus, row.Mergeable)
		result = append(result, row)
	}

	return result, nil
}

// determineCIStatus evaluates the overall CI status from status checks.
func determineCIStatus(checks []struct {
	State      string `json:"state"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
},
) string {
	if len(checks) == 0 {
		return "pending"
	}

	hasFailure := false
	hasPending := false

	for _, check := range checks {
		switch check.Conclusion {
		case "failure", "cancelled", "timed_out", "action_required":
			hasFailure = true
		case "success", "skipped", "neutral":
			// Pass
		default:
			switch check.Status {
			case "queued", "in_progress", "waiting", "pending", "requested":
				hasPending = true
			}
			switch check.State {
			case "FAILURE", "ERROR":
				hasFailure = true
			case "PENDING", "EXPECTED":
				hasPending = true
			}
		}
	}

	if hasFailure {
		return "fail"
	}
	if hasPending {
		return "pending"
	}
	return "pass"
}

// determineMergeableStatus converts GitHub's mergeable field to display value.
func determineMergeableStatus(mergeable string) string {
	switch strings.ToUpper(mergeable) {
	case "MERGEABLE":
		return "ready"
	case "CONFLICTING":
		return "conflict"
	default:
		return "pending"
	}
}

// determineColorClass determines the row color based on CI and merge status.
func determineColorClass(ciStatus, mergeable string) string {
	if ciStatus == "fail" || mergeable == "conflict" {
		return "mq-red"
	}
	if ciStatus == "pending" || mergeable == "pending" {
		return "mq-yellow"
	}
	if ciStatus == "pass" && mergeable == "ready" {
		return "mq-green"
	}
	return "mq-yellow"
}

// FetchWorkers fetches all running worker sessions with activity data.
// Combines gc agent list with tmux session data.
func (f *LiveConvoyFetcher) FetchWorkers() ([]WorkerRow, error) {
	// Get registered agents for context.
	agents, err := f.fetchAgentList()
	if err != nil {
		agents = nil // Non-fatal — fall back to tmux-only data.
	}

	// Build set of known agent session names.
	agentSessions := make(map[string]*agentListEntry)
	for i := range agents {
		sessionName := agents[i].Session
		if sessionName == "" {
			sessionName = agents[i].Name
		}
		agentSessions[sessionName] = &agents[i]
	}

	// Pre-fetch assigned issues map.
	assignedIssues := f.getAssignedIssuesMap()

	// Query all tmux sessions.
	stdout, err := runCmd(f.tmuxCmdTimeout, "tmux", "list-sessions", "-F", "#{session_name}|#{window_activity}")
	if err != nil {
		return nil, nil
	}

	mergeQueueCount := f.getMergeQueueCount()

	var workers []WorkerRow
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")

	for _, line := range lines {
		if line == "" {
			continue
		}

		parts := strings.Split(line, "|")
		if len(parts) < 2 {
			continue
		}

		sessionName := parts[0]

		// Try to match to a known agent.
		agent, isKnown := agentSessions[sessionName]

		// Determine worker name, rig, and agent type.
		var workerName, rig, agentType string
		if isKnown {
			workerName = agent.Name
			rig = agent.Rig
			agentType = agent.Role
		} else {
			// Try to parse session name for rig/role/worker.
			role, parsedRig, worker := parseSessionParts(sessionName)
			_ = role
			workerName = worker
			rig = parsedRig
			agentType = "agent"
		}

		// Parse activity timestamp.
		var activityUnix int64
		if _, err := fmt.Sscanf(parts[1], "%d", &activityUnix); err != nil || activityUnix == 0 {
			continue
		}
		activityTime := time.Unix(activityUnix, 0)
		activityAge := time.Since(activityTime)

		// Get status hint.
		var statusHint string
		if workerName == "refinery" {
			statusHint = getRefineryStatusHint(mergeQueueCount)
		} else {
			statusHint = f.getWorkerStatusHint(sessionName)
		}

		// Look up assigned issue.
		var issueID, issueTitle string
		if issue, ok := assignedIssues[workerName]; ok {
			issueID = issue.ID
			issueTitle = issue.Title
		}
		// Also try rig/pool/worker format.
		if issueID == "" && rig != "" {
			assignee := fmt.Sprintf("%s/polecats/%s", rig, workerName)
			if issue, ok := assignedIssues[assignee]; ok {
				issueID = issue.ID
				issueTitle = issue.Title
			}
		}

		workStatus := calculateWorkerWorkStatus(activityAge, issueID, workerName, f.staleThreshold, f.stuckThreshold)

		workers = append(workers, WorkerRow{
			Name:         workerName,
			Rig:          rig,
			SessionID:    sessionName,
			LastActivity: calculateActivity(activityTime),
			StatusHint:   statusHint,
			IssueID:      issueID,
			IssueTitle:   issueTitle,
			WorkStatus:   workStatus,
			AgentType:    agentType,
		})
	}

	return workers, nil
}

// assignedIssue holds issue info for the assigned issues map.
type assignedIssue struct {
	ID    string
	Title string
}

// getAssignedIssuesMap returns a map of assignee -> assigned issue.
func (f *LiveConvoyFetcher) getAssignedIssuesMap() map[string]assignedIssue {
	result := make(map[string]assignedIssue)

	stdout, err := f.runBdCmd(f.cityPath, "list", "--status=in_progress", "--json")
	if err != nil {
		log.Printf("warning: bd list in_progress failed: %v", err)
		return result
	}

	var issues []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Assignee string `json:"assignee"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &issues); err != nil {
		log.Printf("warning: parsing bd list output: %v", err)
		return result
	}

	for _, issue := range issues {
		if issue.Assignee != "" {
			result[issue.Assignee] = assignedIssue{
				ID:    issue.ID,
				Title: issue.Title,
			}
		}
	}

	return result
}

// calculateWorkerWorkStatus determines the worker's work status based on activity and assignment.
func calculateWorkerWorkStatus(activityAge time.Duration, issueID, workerName string, staleThreshold, stuckThreshold time.Duration) string {
	if workerName == "refinery" {
		return "working"
	}

	if issueID == "" {
		return "idle"
	}

	switch {
	case activityAge < staleThreshold:
		return "working"
	case activityAge < stuckThreshold:
		return "stale"
	default:
		return "stuck"
	}
}

// getWorkerStatusHint captures the last non-empty line from a worker's pane.
func (f *LiveConvoyFetcher) getWorkerStatusHint(sessionName string) string {
	stdout, err := runCmd(f.tmuxCmdTimeout, "tmux", "capture-pane", "-t", sessionName, "-p", "-J")
	if err != nil {
		return ""
	}

	lines := strings.Split(stdout.String(), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			if len(line) > 60 {
				line = line[:57] + "..."
			}
			return line
		}
	}
	return ""
}

// getMergeQueueCount returns the total number of open PRs across all repos.
func (f *LiveConvoyFetcher) getMergeQueueCount() int {
	mergeQueue, err := f.FetchMergeQueue()
	if err != nil {
		return 0
	}
	return len(mergeQueue)
}

// getRefineryStatusHint returns appropriate status for refinery based on merge queue.
func getRefineryStatusHint(mergeQueueCount int) string {
	if mergeQueueCount == 0 {
		return "Idle - Waiting for PRs"
	}
	if mergeQueueCount == 1 {
		return "Processing 1 PR"
	}
	return fmt.Sprintf("Processing %d PRs", mergeQueueCount)
}

// parseActivityTimestamp parses a Unix timestamp string from tmux.
func parseActivityTimestamp(s string) (int64, bool) {
	var unix int64
	if _, err := fmt.Sscanf(s, "%d", &unix); err != nil || unix <= 0 {
		return 0, false
	}
	return unix, true
}

// FetchMail fetches recent mail messages from the beads database.
func (f *LiveConvoyFetcher) FetchMail() ([]MailRow, error) {
	stdout, err := f.runBdCmd(f.cityPath, "list", "--label=gc:message", "--json", "--limit=50")
	if err != nil {
		return nil, fmt.Errorf("listing mail: %w", err)
	}

	var messages []struct {
		ID        string   `json:"id"`
		Title     string   `json:"title"`
		Status    string   `json:"status"`
		CreatedAt string   `json:"created_at"`
		Priority  int      `json:"priority"`
		Assignee  string   `json:"assignee"`
		CreatedBy string   `json:"created_by"`
		Labels    []string `json:"labels"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &messages); err != nil {
		return nil, fmt.Errorf("parsing mail list: %w", err)
	}

	rows := make([]MailRow, 0, len(messages))
	for _, m := range messages {
		var timestamp time.Time
		var age string
		var sortKey int64
		if m.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339, m.CreatedAt); err == nil {
				timestamp = t
				age = formatTimestamp(t)
				sortKey = t.Unix()
			}
		}

		priorityStr := "normal"
		switch m.Priority {
		case 0:
			priorityStr = "urgent"
		case 1:
			priorityStr = "high"
		case 2:
			priorityStr = "normal"
		case 3, 4:
			priorityStr = "low"
		}

		msgType := "notification"
		for _, label := range m.Labels {
			if label == "task" || label == "reply" || label == "scavenge" {
				msgType = label
				break
			}
		}

		from := formatAgentAddress(m.CreatedBy)
		to := formatAgentAddress(m.Assignee)

		rows = append(rows, MailRow{
			ID:        m.ID,
			From:      from,
			FromRaw:   m.CreatedBy,
			To:        to,
			Subject:   m.Title,
			Timestamp: timestamp.Format("15:04"),
			Age:       age,
			Priority:  priorityStr,
			Type:      msgType,
			Read:      m.Status == "closed",
			SortKey:   sortKey,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].SortKey > rows[j].SortKey
	})

	return rows, nil
}

// formatTimestamp formats a time as "Jan 26, 3:45 PM" (or "Jan 26 2006, 3:45 PM" if different year).
func formatTimestamp(t time.Time) string {
	now := time.Now()
	if t.Year() != now.Year() {
		return t.Format("Jan 2 2006, 3:04 PM")
	}
	return t.Format("Jan 2, 3:04 PM")
}

// formatAgentAddress shortens agent addresses for display.
// "rig/polecats/Toast" -> "Toast (rig)"
// "mayor/" -> "Mayor"
func formatAgentAddress(addr string) string {
	if addr == "" {
		return "\u2014" // em-dash
	}
	if addr == "mayor/" || addr == "mayor" {
		return "Mayor"
	}

	parts := strings.Split(addr, "/")
	if len(parts) >= 3 && parts[1] == "polecats" {
		return fmt.Sprintf("%s (%s)", parts[2], parts[0])
	}
	if len(parts) >= 3 && parts[1] == "crew" {
		return fmt.Sprintf("%s (%s/crew)", parts[2], parts[0])
	}
	if len(parts) >= 2 {
		return fmt.Sprintf("%s/%s", parts[0], parts[len(parts)-1])
	}
	return addr
}

// FetchEscalations returns open escalations needing attention.
func (f *LiveConvoyFetcher) FetchEscalations() ([]EscalationRow, error) {
	stdout, err := f.runBdCmd(f.cityPath, "list", "--label=gc:escalation", "--status=open", "--json")
	if err != nil {
		return nil, nil
	}

	var issues []struct {
		ID          string   `json:"id"`
		Title       string   `json:"title"`
		CreatedAt   string   `json:"created_at"`
		CreatedBy   string   `json:"created_by"`
		Labels      []string `json:"labels"`
		Description string   `json:"description"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &issues); err != nil {
		return nil, fmt.Errorf("parsing escalations: %w", err)
	}

	var rows []EscalationRow
	for _, issue := range issues {
		row := EscalationRow{
			ID:          issue.ID,
			Title:       issue.Title,
			EscalatedBy: formatAgentAddress(issue.CreatedBy),
			Severity:    "medium",
		}

		for _, label := range issue.Labels {
			if strings.HasPrefix(label, "severity:") {
				row.Severity = strings.TrimPrefix(label, "severity:")
			}
			if label == "acked" {
				row.Acked = true
			}
		}

		if issue.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339, issue.CreatedAt); err == nil {
				row.Age = formatTimestamp(t)
			}
		}

		rows = append(rows, row)
	}

	severityOrder := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	sort.Slice(rows, func(i, j int) bool {
		si, sj := severityOrder[rows[i].Severity], severityOrder[rows[j].Severity]
		return si < sj
	})

	return rows, nil
}

// FetchHealth returns system health status.
// In Gas City, health info comes from gc events or the controller heartbeat.
func (f *LiveConvoyFetcher) FetchHealth() (*HealthRow, error) {
	row := &HealthRow{}

	// Try to get health info from gc events or controller status.
	// For now, return a basic health row. The controller heartbeat
	// mechanism in Gas City differs from Gas Town's deacon model.
	stdout, err := f.runGcCmd("status", "--json")
	if err != nil {
		row.DeaconHeartbeat = "no heartbeat"
		return row, nil
	}

	var status statusJSON
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		row.DeaconHeartbeat = "parse error"
		return row, nil
	}

	// Count agents for health display.
	agents, _ := f.fetchAgentList()
	for _, agent := range agents {
		if agent.Status == "running" || agent.Status == "idle" {
			row.HealthyAgents++
		} else {
			row.UnhealthyAgents++
		}
	}

	row.DeaconHeartbeat = "active"
	row.HeartbeatFresh = true

	return row, nil
}

// FetchQueues returns work queues and their status.
func (f *LiveConvoyFetcher) FetchQueues() ([]QueueRow, error) {
	stdout, err := f.runBdCmd(f.cityPath, "list", "--label=gc:queue", "--json")
	if err != nil {
		return nil, nil
	}

	var queues []struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Status      string `json:"status"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &queues); err != nil {
		return nil, fmt.Errorf("parsing queues: %w", err)
	}

	var rows []QueueRow
	for _, q := range queues {
		row := QueueRow{
			Name:   q.Title,
			Status: q.Status,
		}

		for _, line := range strings.Split(q.Description, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "available_count:") {
				_, _ = fmt.Sscanf(line, "available_count: %d", &row.Available)
			} else if strings.HasPrefix(line, "processing_count:") {
				_, _ = fmt.Sscanf(line, "processing_count: %d", &row.Processing)
			} else if strings.HasPrefix(line, "completed_count:") {
				_, _ = fmt.Sscanf(line, "completed_count: %d", &row.Completed)
			} else if strings.HasPrefix(line, "failed_count:") {
				_, _ = fmt.Sscanf(line, "failed_count: %d", &row.Failed)
			} else if strings.HasPrefix(line, "status:") {
				var s string
				_, _ = fmt.Sscanf(line, "status: %s", &s)
				if s != "" {
					row.Status = s
				}
			}
		}

		rows = append(rows, row)
	}

	return rows, nil
}

// FetchHooks returns all hooked beads (work pinned to agents).
func (f *LiveConvoyFetcher) FetchHooks() ([]HookRow, error) {
	stdout, err := f.runBdCmd(f.cityPath, "list", "--status=hooked", "--json", "--limit=0")
	if err != nil {
		return nil, nil
	}

	var beadList []struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Assignee  string `json:"assignee"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &beadList); err != nil {
		return nil, fmt.Errorf("parsing hooked beads: %w", err)
	}

	var rows []HookRow
	for _, bead := range beadList {
		row := HookRow{
			ID:       bead.ID,
			Title:    bead.Title,
			Assignee: bead.Assignee,
			Agent:    formatAgentAddress(bead.Assignee),
		}

		if bead.UpdatedAt != "" {
			if t, err := time.Parse(time.RFC3339, bead.UpdatedAt); err == nil {
				age := time.Since(t)
				row.Age = formatTimestamp(t)
				row.IsStale = age > time.Hour
			}
		}

		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].IsStale != rows[j].IsStale {
			return rows[i].IsStale
		}
		return rows[i].Age > rows[j].Age
	})

	return rows, nil
}

// FetchIssues returns open issues (the backlog).
func (f *LiveConvoyFetcher) FetchIssues() ([]IssueRow, error) {
	var allBeads []struct {
		ID        string   `json:"id"`
		Title     string   `json:"title"`
		Type      string   `json:"type"`
		Priority  int      `json:"priority"`
		Labels    []string `json:"labels"`
		CreatedAt string   `json:"created_at"`
	}

	// Fetch open issues.
	if stdout, err := f.runBdCmd(f.cityPath, "list", "--status=open", "--json", "--limit=50"); err == nil {
		var openBeads []struct {
			ID        string   `json:"id"`
			Title     string   `json:"title"`
			Type      string   `json:"type"`
			Priority  int      `json:"priority"`
			Labels    []string `json:"labels"`
			CreatedAt string   `json:"created_at"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &openBeads); err == nil {
			allBeads = append(allBeads, openBeads...)
		}
	}

	// Fetch hooked issues (in progress).
	if stdout, err := f.runBdCmd(f.cityPath, "list", "--status=hooked", "--json", "--limit=50"); err == nil {
		var hookedBeads []struct {
			ID        string   `json:"id"`
			Title     string   `json:"title"`
			Type      string   `json:"type"`
			Priority  int      `json:"priority"`
			Labels    []string `json:"labels"`
			CreatedAt string   `json:"created_at"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &hookedBeads); err == nil {
			allBeads = append(allBeads, hookedBeads...)
		}
	}

	var rows []IssueRow
	for _, bead := range allBeads {
		// Skip internal types (messages, convoys, queues, merge-requests, wisps).
		isInternal := false
		switch bead.Type {
		case "message", "convoy", "queue", "merge-request", "wisp", "agent":
			isInternal = true
		}
		for _, l := range bead.Labels {
			switch l {
			case "gc:message", "gc:convoy", "gc:queue", "gc:merge-request", "gc:wisp", "gc:agent":
				isInternal = true
			}
		}
		if isInternal {
			continue
		}

		row := IssueRow{
			ID:       bead.ID,
			Title:    bead.Title,
			Type:     bead.Type,
			Priority: bead.Priority,
		}

		var displayLabels []string
		for _, label := range bead.Labels {
			if !strings.HasPrefix(label, "gc:") && !strings.HasPrefix(label, "internal:") {
				displayLabels = append(displayLabels, label)
			}
		}
		if len(displayLabels) > 0 {
			row.Labels = strings.Join(displayLabels, ", ")
			if len(row.Labels) > 25 {
				row.Labels = row.Labels[:22] + "..."
			}
		}

		if bead.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339, bead.CreatedAt); err == nil {
				row.Age = formatTimestamp(t)
			}
		}

		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		pi, pj := rows[i].Priority, rows[j].Priority
		if pi == 0 {
			pi = 5
		}
		if pj == 0 {
			pj = 5
		}
		if pi != pj {
			return pi < pj
		}
		return rows[i].Age > rows[j].Age
	})

	return rows, nil
}

// FetchActivity returns recent activity using gc events --json --since 1h.
func (f *LiveConvoyFetcher) FetchActivity() ([]ActivityRow, error) {
	stdout, err := f.runGcCmd("events", "--json", "--since", "1h")
	if err != nil {
		return nil, nil
	}

	// gc events --json returns JSONL (one JSON object per line).
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) == 0 {
		return nil, nil
	}

	// Take last 50 events for richer timeline.
	start := 0
	if len(lines) > 50 {
		start = len(lines) - 50
	}

	var rows []ActivityRow
	for i := len(lines) - 1; i >= start; i-- {
		line := lines[i]
		if line == "" {
			continue
		}

		var event struct {
			Timestamp  string                 `json:"ts"`
			Type       string                 `json:"type"`
			Actor      string                 `json:"actor"`
			Payload    map[string]interface{} `json:"payload"`
			Visibility string                 `json:"visibility"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		if event.Visibility == "audit" {
			continue
		}

		row := ActivityRow{
			Type:         event.Type,
			Category:     eventCategory(event.Type),
			Actor:        formatAgentAddress(event.Actor),
			Rig:          extractRig(event.Actor),
			Icon:         eventIcon(event.Type),
			RawTimestamp: event.Timestamp,
		}

		if t, err := time.Parse(time.RFC3339, event.Timestamp); err == nil {
			row.Time = formatTimestamp(t)
		}

		row.Summary = eventSummary(event.Type, event.Actor, event.Payload)

		rows = append(rows, row)
	}

	return rows, nil
}

// eventCategory classifies an event type into a filter category.
func eventCategory(eventType string) string {
	switch eventType {
	case "spawn", "kill", "session_start", "session_end", "session_death", "mass_death", "nudge", "handoff":
		return "agent"
	case "sling", "hook", "unhook", "done", "merge_started", "merged", "merge_failed":
		return "work"
	case "mail", "escalation_sent", "escalation_acked", "escalation_closed":
		return "comms"
	case "boot", "halt", "patrol_started", "patrol_complete":
		return "system"
	default:
		return "system"
	}
}

// extractRig extracts the rig name from an actor address like "myrig/polecats/nux".
func extractRig(actor string) string {
	if actor == "" {
		return ""
	}
	parts := strings.SplitN(actor, "/", 2)
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

// eventIcon returns an emoji for an event type.
func eventIcon(eventType string) string {
	icons := map[string]string{
		"sling":             "\U0001f3af", // target
		"hook":              "\U0001fa9d", // hook
		"unhook":            "\U0001f513", // unlocked
		"done":              "\u2705",     // check mark
		"mail":              "\U0001f4ec", // mailbox
		"spawn":             "\U0001f9a8", // skunk (polecat)
		"kill":              "\U0001f480", // skull
		"nudge":             "\U0001f449", // pointing right
		"handoff":           "\U0001f91d", // handshake
		"session_start":     "\u25b6\ufe0f",
		"session_end":       "\u23f9\ufe0f",
		"session_death":     "\u2620\ufe0f",
		"mass_death":        "\U0001f4a5", // collision
		"patrol_started":    "\U0001f50d", // magnifying glass
		"patrol_complete":   "\u2714\ufe0f",
		"escalation_sent":   "\u26a0\ufe0f",
		"escalation_acked":  "\U0001f44d", // thumbs up
		"escalation_closed": "\U0001f515", // bell slash
		"merge_started":     "\U0001f500", // shuffle
		"merged":            "\u2728",     // sparkles
		"merge_failed":      "\u274c",     // cross mark
		"boot":              "\U0001f680", // rocket
		"halt":              "\U0001f6d1", // stop sign
	}
	if icon, ok := icons[eventType]; ok {
		return icon
	}
	return "\U0001f4cb" // clipboard
}

// eventSummary generates a human-readable summary for an event.
func eventSummary(eventType, actor string, payload map[string]interface{}) string {
	shortActor := formatAgentAddress(actor)

	switch eventType {
	case "sling":
		bead, _ := payload["bead"].(string)
		target, _ := payload["target"].(string)
		return fmt.Sprintf("%s slung to %s", bead, formatAgentAddress(target))
	case "done":
		bead, _ := payload["bead"].(string)
		return fmt.Sprintf("%s completed %s", shortActor, bead)
	case "mail":
		to, _ := payload["to"].(string)
		subject, _ := payload["subject"].(string)
		if len(subject) > 25 {
			subject = subject[:22] + "..."
		}
		return fmt.Sprintf("\u2192 %s: %s", formatAgentAddress(to), subject)
	case "spawn":
		return fmt.Sprintf("%s spawned", shortActor)
	case "kill":
		return fmt.Sprintf("%s killed", shortActor)
	case "hook":
		bead, _ := payload["bead"].(string)
		return fmt.Sprintf("%s hooked %s", shortActor, bead)
	case "unhook":
		bead, _ := payload["bead"].(string)
		return fmt.Sprintf("%s unhooked %s", shortActor, bead)
	case "merged":
		branch, _ := payload["branch"].(string)
		return fmt.Sprintf("merged %s", branch)
	case "merge_failed":
		reason, _ := payload["reason"].(string)
		if len(reason) > 30 {
			reason = reason[:27] + "..."
		}
		return fmt.Sprintf("merge failed: %s", reason)
	case "escalation_sent":
		return "escalation created"
	case "session_death":
		role, _ := payload["role"].(string)
		return fmt.Sprintf("%s session died", formatAgentAddress(role))
	case "mass_death":
		count, _ := payload["count"].(float64)
		return fmt.Sprintf("%.0f sessions died", count)
	default:
		return eventType
	}
}
