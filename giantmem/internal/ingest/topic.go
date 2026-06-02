package ingest

import (
	"database/sql"
	"regexp"
	"strings"
)

// LookupTopicOverride returns a pinned topic for sessionID, or "" if none.
// Pinned topics survive re-ingest (DetectTopic skipped when override present).
func LookupTopicOverride(db *sql.DB, sessionID string) string {
	if db == nil || sessionID == "" {
		return ""
	}
	var topic string
	err := db.QueryRow(
		`SELECT topic FROM session_topic_overrides WHERE session_id = ?`,
		sessionID,
	).Scan(&topic)
	if err != nil {
		return ""
	}
	return topic
}

var topicKeywords = map[string][]string{
	"auth":      {"auth", "login", "jwt", "token", "password", "credential", "oauth"},
	"api":       {"api", "endpoint", "route", "rest", "graphql"},
	"database":  {"database", "sql", "query", "migration", "model", "schema"},
	"test":      {"test", "pytest", "jest", "coverage", "mock", "fixture"},
	"bug":       {"bug", "fix", "error", "debug", "broken", "failing"},
	"feature":   {"feature", "implement", "add", "create", "build"},
	"refactor":  {"refactor", "cleanup", "reorganize", "restructure", "rename"},
	"config":    {"config", "setting", "env", "environment", "setup"},
	"docs":      {"document", "readme", "explain", "describe"},
	"perf":      {"performance", "optimize", "speed", "slow", "cache"},
	"ui":        {"ui", "frontend", "component", "style", "css", "render"},
	"deploy":    {"deploy", "ci", "cd", "pipeline", "docker"},
	"workspace": {"workspace", "giantmem", "hook", "session", "mcp", "plugin"},
}

var keywordRegexCache = map[string]*regexp.Regexp{}

func keywordRegex(kw string) *regexp.Regexp {
	if re, ok := keywordRegexCache[kw]; ok {
		return re
	}
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(kw) + `\w*\b`)
	keywordRegexCache[kw] = re
	return re
}

// DetectTopic scores text against known topic keywords. Returns the top topic
// or "general" when no topic exceeds the threshold.
func DetectTopic(text string) string {
	lower := strings.ToLower(text)
	bestTopic := "general"
	bestScore := 0
	for topic, kws := range topicKeywords {
		score := 0
		for _, kw := range kws {
			score += len(keywordRegex(kw).FindAllStringIndex(lower, -1))
		}
		if score > bestScore {
			bestScore = score
			bestTopic = topic
		}
	}
	if bestScore <= 2 {
		return "general"
	}
	return bestTopic
}
