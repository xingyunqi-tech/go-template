//go:build parity

package parity_test

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const _feedbacksURL = "/api/v1/feedbacks"

// postgresContainers are the PostgreSQL containers of the Go and nestjs services, found by their
// compose project unless PARITY_GO_PG_CONTAINER / PARITY_NESTJS_PG_CONTAINER name them.
func postgresContainers(t *testing.T) [2]string {
	t.Helper()

	var out [2]string

	for i, side := range []struct{ env, project string }{
		{"PARITY_GO_PG_CONTAINER", "go-template-parity"},
		{"PARITY_NESTJS_PG_CONTAINER", "go-template-parity-nestjs"},
	} {
		if v := os.Getenv(side.env); v != "" {
			out[i] = v

			continue
		}

		id, err := exec.CommandContext(t.Context(), "docker", "ps", "-q", //nolint:gosec // fixed docker arguments
			"--filter", "label=com.docker.compose.project="+side.project,
			"--filter", "label=com.docker.compose.service=postgres").Output()
		require.NoError(t, err)

		out[i] = strings.TrimSpace(string(id))
		require.NotEmpty(t, out[i], side.project)
	}

	return out
}

// psql runs sql in the database of one service and returns its unaligned output.
func psql(ctx context.Context, t *testing.T, container, sql string) string {
	t.Helper()

	out, err := exec.CommandContext(ctx, "docker", "exec", container, "psql", "-U", "root", "-d", "api", "-qAtc", sql).Output()
	require.NoError(t, err, sql)

	return strings.TrimSpace(string(out))
}

// userIDs returns the user id of each side's token.
func userIDs(t *testing.T, tokens [2]string) [2]int {
	t.Helper()

	p := do(t, http.MethodGet, "/api/v1/auth/me", withTokens(tokens))
	goID, _ := dataOf(t, p.Go)["id"].(float64)
	nestID, _ := dataOf(t, p.Nestjs)["id"].(float64)

	return [2]int{int(goID), int(nestID)}
}

func insertReport(t *testing.T, users [2]int, kind string) [2]int {
	t.Helper()

	var ids [2]int

	for i, container := range postgresContainers(t) {
		out := psql(t.Context(), t, container, `INSERT INTO "feedback" ("userId", "type", "content", "questionVersion", "reason") `+
			"VALUES ("+strconv.Itoa(users[i])+", '"+kind+"', 'r', 3, 'other') RETURNING id")

		id, err := strconv.Atoi(strings.Fields(out)[0])
		require.NoError(t, err, out)

		ids[i] = id
	}

	return ids
}

func TestFeedbacksRequireAuthentication(t *testing.T) {
	t.Parallel()

	for _, r := range [][2]string{{http.MethodGet, ""}, {http.MethodPost, ""}, {http.MethodGet, "/1"}} {
		t.Run(r[0]+r[1], func(t *testing.T) {
			t.Parallel()

			requireSameOwned(t, do(t, r[0], _feedbacksURL+r[1], withJSON(t, map[string]any{})))
		})
	}
}

func TestFeedbackCreateValidation(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "feedback.validation")
	bodies := []map[string]any{
		{},
		{"type": "question_error", "content": ""},
		{"type": "bug", "content": strings.Repeat("c", 2001), "contact": 1},
		{"type": "bug", "content": "c", "images": []any{1, "a", "b", "c", "d", "e"}},
		{"type": "bug", "content": "c", "images": "a"},
		{"type": "bug", "content": "c", "images": []any{strings.Repeat("x", 1001)}},
	}

	for _, lang := range []string{"en", "zh"} {
		for i, body := range bodies {
			t.Run(lang+"/"+strconv.Itoa(i), func(t *testing.T) {
				t.Parallel()

				requireSameOwned(t, do(t, http.MethodPost, _feedbacksURL, withTokens(tokens), withJSON(t, body), withHeader("Accept-Language", lang)))
			})
		}
	}
}

func TestFeedbackListValidationAndFilters(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "feedback.list")

	for _, q := range []string{"?type=ai_content&status=x&page=0&limit=a", "?page=1e400", "?type=question_error", "?status=closed"} {
		t.Run(q, func(t *testing.T) {
			t.Parallel()

			requireSameOwned(t, do(t, http.MethodGet, _feedbacksURL+q, withTokens(tokens)))
		})
	}
}

func TestFeedbackUnknownOrInvalidID(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "feedback.ids")

	for _, path := range []string{"/abc", "/0", "/999999"} {
		requireSameOwned(t, do(t, http.MethodGet, _feedbacksURL+path, withTokens(tokens)))
	}
}

func TestFeedbackLifecycle(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "feedback.lifecycle")
	call := func(method, target string, options ...option) pair {
		return requireSameOwned(t, do(t, method, target, append([]option{withTokens(tokens)}, options...)...))
	}

	first := createdIDs(t, call(http.MethodPost, _feedbacksURL, withJSON(t, map[string]any{"type": "bug", "content": "c"})))
	call(http.MethodPost, _feedbacksURL, withJSON(t, map[string]any{
		"type": "suggestion", "content": "<b>s</b>", "images": []string{"a", "b"}, "contact": "x", "unknown": 1,
	}))
	call(http.MethodPost, _feedbacksURL, withJSON(t, map[string]any{"type": "other", "content": "o", "images": []string{}}))

	targets := itemTargets(_feedbacksURL, first, "")
	call(http.MethodGet, targets[0], withTargets(targets))

	for _, q := range []string{"", "?type=bug", "?status=pending&limit=2", "?page=1.3&limit=1", "?page=2&limit=1.5", "?limit=100"} {
		call(http.MethodGet, _feedbacksURL+q)
	}

	admin := loginBoth(t, "admin@example.com")
	requireSameOwned(t, do(t, http.MethodGet, targets[0], withTargets(targets), withTokens(admin)))
}

func TestFeedbackReportTypesVisibility(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "feedback.reports")
	users := userIDs(t, tokens)
	call := func(target string, options ...option) {
		requireSameOwned(t, do(t, http.MethodGet, target, append([]option{withTokens(tokens)}, options...)...))
	}

	for _, kind := range []string{"question_error", "ai_content"} {
		targets := itemTargets(_feedbacksURL, insertReport(t, users, kind), "")
		call(targets[0], withTargets(targets))
	}

	call(_feedbacksURL)
	call(_feedbacksURL + "?type=question_error")
}
