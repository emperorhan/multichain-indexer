package ci_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAgentAutoMergeWorkflowYAMLIsParseable(t *testing.T) {
	_, workflow := readAgentAutoMergeWorkflow(t)

	jobs := mustMap(t, workflow["jobs"], "jobs")
	autoMergeJob := mustMap(t, jobs["auto-merge"], "jobs.auto-merge")
	steps := mustSlice(t, autoMergeJob["steps"], "jobs.auto-merge.steps")

	hasGitHubScriptStep := false
	for idx, stepRaw := range steps {
		step := mustMap(t, stepRaw, "jobs.auto-merge.steps["+strconv.Itoa(idx)+"]")
		uses, _ := step["uses"].(string)
		if !strings.HasPrefix(uses, "actions/github-script@") {
			continue
		}

		with := mustMap(t, step["with"], "jobs.auto-merge.steps["+strconv.Itoa(idx)+"].with")
		script, _ := with["script"].(string)
		if strings.TrimSpace(script) == "" {
			t.Fatalf("jobs.auto-merge.steps[%d].with.script must not be empty", idx)
		}
		hasGitHubScriptStep = true
	}

	if !hasGitHubScriptStep {
		t.Fatal("jobs.auto-merge must include an actions/github-script step")
	}
}

func TestAgentAutoMergeWorkflowUsesReadOnlyIssuePermission(t *testing.T) {
	_, workflow := readAgentAutoMergeWorkflow(t)

	permissions := mustMap(t, workflow["permissions"], "permissions")
	issuesPermission, _ := permissions["issues"].(string)
	if issuesPermission != "read" {
		t.Fatalf("permissions.issues = %q, want %q", issuesPermission, "read")
	}
}

func TestAgentAutoMergeWorkflowAvoidsFragileScopeCommentPath(t *testing.T) {
	raw, _ := readAgentAutoMergeWorkflow(t)
	body := string(raw)

	// Guard against reintroducing the legacy workflow-scope comment helper path
	// that made the Agent Auto Merge script fragile in prior failures.
	disallowed := []string{
		"HAS_AGENT_GH_TOKEN:",
		"ensureWorkflowScopeComment(",
		"workflowScopeMarker",
	}
	for _, token := range disallowed {
		if strings.Contains(body, token) {
			t.Fatalf("agent-auto-merge workflow must not contain %q", token)
		}
	}
}

func readAgentAutoMergeWorkflow(t *testing.T) ([]byte, map[string]any) {
	t.Helper()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("unable to resolve test file path")
	}

	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	workflowPath := filepath.Join(repoRoot, ".github", "workflows", "agent-auto-merge.yml")

	raw, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read %s: %v", workflowPath, err)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse %s: %v", workflowPath, err)
	}

	return raw, parsed
}

func mustMap(t *testing.T, value any, path string) map[string]any {
	t.Helper()

	m, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s must be a map, got %T", path, value)
	}
	return m
}

func mustSlice(t *testing.T, value any, path string) []any {
	t.Helper()

	list, ok := value.([]any)
	if !ok {
		t.Fatalf("%s must be a list, got %T", path, value)
	}
	return list
}
