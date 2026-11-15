package agentcreds

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// claudeRouteEnvKeys are the variables that say where Claude Code sends model
// requests. Unlike credentials, these may also come from the env block of the
// user's own settings.json, because Claude Code applies that block itself.
var claudeRouteEnvKeys = []string{
	"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
	"ANTHROPIC_BASE_URL", "ANTHROPIC_BEDROCK_BASE_URL", "ANTHROPIC_VERTEX_BASE_URL", "ANTHROPIC_FOUNDRY_BASE_URL",
	"ANTHROPIC_FOUNDRY_RESOURCE",
	"CLAUDE_CODE_SKIP_BEDROCK_AUTH", "CLAUDE_CODE_SKIP_VERTEX_AUTH", "CLAUDE_CODE_SKIP_FOUNDRY_AUTH",
	"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_PROFILE", "CLOUD_ML_REGION",
}

// claudeRoutePresenceKeys are credentials whose presence decides a route's
// credential mode. Only "set" is kept, never the value.
var claudeRoutePresenceKeys = []string{"AWS_BEARER_TOKEN_BEDROCK", "ANTHROPIC_FOUNDRY_API_KEY"}

// ClaudeRouteEnv is the route configuration Claude Code will apply: the
// launch or process environment first, then the user's settings.json env
// block. It holds no credential values.
type ClaudeRouteEnv struct {
	values       map[string]string
	fromSettings map[string]bool
	// SettingsPath is the user settings file that was read, if any.
	SettingsPath string
	// APIKeyHelper is true when the user settings name an apiKeyHelper, so
	// Claude Code gets a key from a command AO never runs.
	APIKeyHelper bool
}

// ResolveClaudeRouteEnv reads the route variables for a launch context. opts
// is the launch context before settings are applied (CommandEnv over the
// process environment); project settings are never read for routes.
func ResolveClaudeRouteEnv(ctx context.Context, opts ResolveOptions) ClaudeRouteEnv {
	lookup := func(key string) string {
		if value, ok := opts.CommandEnv[key]; ok {
			return strings.TrimSpace(value)
		}
		return opts.Getenv(key)
	}
	pathOptions := opts
	pathOptions.Env = lookup
	route := ClaudeRouteEnv{values: map[string]string{}, fromSettings: map[string]bool{}}
	var settings map[string]string
	if dir, err := claudeConfigDir(pathOptions); err == nil && dir != "" {
		route.SettingsPath = filepath.Join(dir, "settings.json")
		settings, route.APIKeyHelper = readClaudeRouteSettings(ctx, route.SettingsPath)
	}
	for _, key := range append(append([]string{}, claudeRouteEnvKeys...), claudeRoutePresenceKeys...) {
		value, fromSettings := lookup(key), false
		if value == "" && settings[key] != "" {
			value, fromSettings = settings[key], true
		}
		if value == "" {
			continue
		}
		if slices.Contains(claudeRoutePresenceKeys, key) {
			value = "set"
		}
		route.values[key], route.fromSettings[key] = value, fromSettings
	}
	return route
}

// Get returns a route variable, or "" when unset. Presence-only variables
// read "set".
func (e ClaudeRouteEnv) Get(key string) string { return e.values[key] }

// FromSettings reports whether key came from the user settings file rather
// than the environment.
func (e ClaudeRouteEnv) FromSettings(key string) bool { return e.fromSettings[key] }

// Truthy reports whether a flag variable is set to a true value.
func (e ClaudeRouteEnv) Truthy(key string) bool { return EnvTruthy(e.values[key]) }

// EnvTruthy reports whether a flag value is true, as Claude Code reads it.
func EnvTruthy(value string) bool { return envTruthy(value) }

// Getenv reads a variable the way the resolver does: the injected lookup,
// else the process environment, trimmed.
func (o ResolveOptions) Getenv(name string) string { return o.env(name) }

func readClaudeRouteSettings(ctx context.Context, path string) (map[string]string, bool) {
	if ctx.Err() != nil {
		return nil, false
	}
	file, err := os.Open(path) //nolint:gosec // path is the documented user settings location
	if err != nil {
		return nil, false
	}
	defer func() { _ = file.Close() }()
	const limit = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(raw) > limit {
		return nil, false
	}
	var payload struct {
		Env          map[string]json.RawMessage `json:"env"`
		APIKeyHelper json.RawMessage            `json:"apiKeyHelper"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return nil, false
	}
	values := map[string]string{}
	for _, key := range append(append([]string{}, claudeRouteEnvKeys...), claudeRoutePresenceKeys...) {
		rawValue, ok := payload.Env[key]
		if !ok {
			continue
		}
		var text string
		var flag bool
		var number json.Number
		switch {
		case json.Unmarshal(rawValue, &text) == nil:
			values[key] = strings.TrimSpace(text)
		case json.Unmarshal(rawValue, &flag) == nil:
			values[key] = strconv.FormatBool(flag)
		case json.Unmarshal(rawValue, &number) == nil:
			values[key] = number.String()
		}
	}
	var helper string
	hasHelper := json.Unmarshal(payload.APIKeyHelper, &helper) == nil && strings.TrimSpace(helper) != ""
	return values, hasHelper
}
