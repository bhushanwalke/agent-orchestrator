package opencode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/jsonc"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/modelroute"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ConfigHome is the directory holding the user's opencode config directory:
// XDG_CONFIG_HOME, else ~/.config. It is a variable so tests never read the
// developer's own config.
var ConfigHome = func() string {
	if dir := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config")
}

// ManagedConfigDirs are the system-wide directories an administrator's
// managed opencode config lives in, highest precedence. A variable for tests.
var ManagedConfigDirs = defaultManagedConfigDirs()

func defaultManagedConfigDirs() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"/Library/Application Support/opencode"}
	case "windows":
		if dir := os.Getenv("ProgramData"); dir != "" {
			return []string{filepath.Join(dir, "opencode")}
		}
		return nil
	default:
		return []string{"/etc/opencode"}
	}
}

const (
	maxConfigBytes   = 4 << 20
	maxRouteChecks   = 8
	maxProviderCount = domain.MaxAgentModelRoutes
)

type configLayer struct {
	source string
	raw    []byte
}

// configLayers reads opencode's user-level config layers, lowest precedence
// first. Project files are not read: they differ per worktree.
func configLayers(ctx context.Context) []configLayer {
	var layers []configLayer
	readFile := func(path string) {
		if path == "" {
			return
		}
		file, err := os.Open(path) //nolint:gosec // documented opencode config locations
		if err != nil {
			return
		}
		defer func() { _ = file.Close() }()
		raw, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
		if err != nil || len(raw) > maxConfigBytes {
			return
		}
		layers = append(layers, configLayer{source: authprobe.FileSource(path).Source, raw: raw})
	}
	if home := ConfigHome(); home != "" {
		for _, name := range []string{"config.json", "opencode.json", "opencode.jsonc"} {
			readFile(filepath.Join(home, "opencode", name))
		}
	}
	readFile(strings.TrimSpace(authprobe.Getenv(ctx, "OPENCODE_CONFIG")))
	if dir := strings.TrimSpace(authprobe.Getenv(ctx, "OPENCODE_CONFIG_DIR")); dir != "" {
		readFile(filepath.Join(dir, "opencode.json"))
		readFile(filepath.Join(dir, "opencode.jsonc"))
	}
	if content := authprobe.Getenv(ctx, "OPENCODE_CONFIG_CONTENT"); strings.TrimSpace(content) != "" && len(content) <= maxConfigBytes {
		layers = append(layers, configLayer{source: "OPENCODE_CONFIG_CONTENT", raw: []byte(content)})
	}
	for _, dir := range ManagedConfigDirs {
		readFile(filepath.Join(dir, "opencode.json"))
		readFile(filepath.Join(dir, "opencode.jsonc"))
	}
	return layers
}

type providerConfig struct {
	id           string
	npm          string
	env          []string
	baseURL      string
	apiKey       string
	configSource string
	isDefault    bool
}

// configuredProviders merges provider.<id> across the layers and keeps the
// providers with a baseURL, the root model's provider first, then by id.
func configuredProviders(ctx context.Context) []providerConfig {
	merged := map[string]*providerConfig{}
	defaultProvider := ""
	for _, layer := range configLayers(ctx) {
		var config struct {
			Model    string `json:"model"`
			Provider map[string]struct {
				NPM     string   `json:"npm"`
				Env     []string `json:"env"`
				Options struct {
					BaseURL string  `json:"baseURL"`
					APIKey  *string `json:"apiKey"`
				} `json:"options"`
			} `json:"provider"`
		}
		if json.Unmarshal(jsonc.Strip(layer.raw), &config) != nil {
			continue
		}
		if provider, _, ok := strings.Cut(strings.TrimSpace(config.Model), "/"); ok && provider != "" {
			defaultProvider = provider
		}
		for id, entry := range config.Provider {
			current := merged[id]
			if current == nil {
				current = &providerConfig{id: id}
				merged[id] = current
			}
			if entry.NPM != "" {
				current.npm = entry.NPM
			}
			if len(entry.Env) > 0 {
				current.env = entry.Env
			}
			if base := strings.TrimSpace(entry.Options.BaseURL); base != "" {
				current.baseURL, current.configSource = base, layer.source
			}
			if entry.Options.APIKey != nil {
				current.apiKey = strings.TrimSpace(*entry.Options.APIKey)
			}
		}
	}
	providers := make([]providerConfig, 0, len(merged))
	for _, provider := range merged {
		if provider.baseURL == "" {
			continue
		}
		provider.isDefault = provider.id == defaultProvider
		providers = append(providers, *provider)
	}
	sort.Slice(providers, func(i, j int) bool {
		if providers[i].isDefault != providers[j].isDefault {
			return providers[i].isDefault
		}
		return providers[i].id < providers[j].id
	})
	if len(providers) > maxProviderCount {
		providers = providers[:maxProviderCount]
	}
	return providers
}

// familyForNPM maps a provider's AI SDK package to the API it speaks.
func familyForNPM(npm string) domain.AgentModelRouteFamily {
	switch npm = strings.TrimSpace(npm); {
	case npm == "" || npm == "@ai-sdk/openai-compatible":
		return domain.AgentModelRouteFamilyOpenAICompatible
	case npm == "@ai-sdk/openai":
		return domain.AgentModelRouteFamilyOpenAI
	case npm == "@ai-sdk/anthropic":
		return domain.AgentModelRouteFamilyAnthropic
	case npm == "@ai-sdk/amazon-bedrock":
		return domain.AgentModelRouteFamilyBedrock
	case strings.HasPrefix(npm, "@ai-sdk/google-vertex"):
		return domain.AgentModelRouteFamilyVertex
	case strings.HasPrefix(npm, "@ai-sdk/google"):
		return domain.AgentModelRouteFamilyGoogle
	case npm == "@ai-sdk/azure":
		return domain.AgentModelRouteFamilyFoundry
	default:
		return domain.AgentModelRouteFamilyOther
	}
}

var envReference = regexp.MustCompile(`^\{env:([A-Za-z_][A-Za-z0-9_]*)\}$`)

// checkProvider records one provider's route, checked with exactly the key
// opencode would send when AO can see it.
func checkProvider(ctx context.Context, provider providerConfig, stored map[string]bool) {
	route := domain.AgentModelRoute{
		Name:         provider.id,
		APIFamily:    familyForNPM(provider.npm),
		Host:         domain.RouteHost(provider.baseURL),
		Credential:   domain.AgentModelRouteCredentialNone,
		ConfigSource: provider.configSource,
		IsDefault:    provider.isDefault,
	}
	route.Kind = modelroute.Kind(route.Host)
	key := ""
	switch {
	case envReference.MatchString(provider.apiKey):
		variable := envReference.FindStringSubmatch(provider.apiKey)[1]
		route.Credential, route.CredentialSource = domain.AgentModelRouteCredentialAPIKey, variable
		if key = strings.TrimSpace(authprobe.Getenv(ctx, variable)); key == "" {
			modelroute.RecordMissingCredential(ctx, route)
			return
		}
	case strings.HasPrefix(provider.apiKey, "{file:"):
		// AO does not read key files; the route is identified, not checked.
		route.Credential, route.CredentialSource = domain.AgentModelRouteCredentialAPIKey, provider.configSource
		modelroute.Record(ctx, route, modelroute.Result{Verification: domain.AgentModelRouteNotAttempted})
		return
	case provider.apiKey != "":
		route.Credential, route.CredentialSource, key = domain.AgentModelRouteCredentialAPIKey, provider.configSource, provider.apiKey
	case stored[provider.id]:
		// opencode sends the key from its own store, which AO cannot see.
		route.Credential = domain.AgentModelRouteCredentialUnknown
		modelroute.Record(ctx, route, modelroute.Result{Verification: domain.AgentModelRouteNotAttempted})
		return
	default:
		for _, variable := range provider.env {
			if value := strings.TrimSpace(authprobe.Getenv(ctx, variable)); value != "" {
				route.Credential, route.CredentialSource, key = domain.AgentModelRouteCredentialAPIKey, variable, value
				break
			}
		}
	}
	headers := http.Header{}
	if key != "" {
		if route.APIFamily == domain.AgentModelRouteFamilyAnthropic {
			headers.Set("x-api-key", key)
		} else {
			headers.Set("Authorization", "Bearer "+key)
		}
	}
	modelroute.Record(ctx, route, modelroute.Check(ctx, modelroute.Request{BaseURL: provider.baseURL, Family: route.APIFamily, Headers: headers}))
}

// CheckProviderRoutes identifies and checks the providers opencode's config
// points at a base URL, at most maxRouteChecks at a time. Each check collects
// into its own evidence, which is then replayed onto ctx in provider order so
// routes and diagnostics read the same on every run. stored names providers
// with a credential in opencode's own store.
func CheckProviderRoutes(ctx context.Context, stored map[string]bool) []domain.AgentModelRoute {
	providers := configuredProviders(ctx)
	collected := make([]*ports.AuthEvidence, len(providers))
	limit := make(chan struct{}, maxRouteChecks)
	var wg sync.WaitGroup
	for i, provider := range providers {
		checkCtx, evidence := ports.WithAuthEvidence(ctx)
		collected[i] = evidence
		wg.Add(1)
		go func() {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			checkProvider(checkCtx, provider, stored)
		}()
	}
	wg.Wait()
	var routes []domain.AgentModelRoute
	for _, evidence := range collected {
		for _, route := range evidence.Routes() {
			ports.RecordModelRoute(ctx, route)
			routes = append(routes, route)
		}
		for _, step := range evidence.Diagnostics() {
			ports.RecordAuthDiagnostic(ctx, step)
		}
	}
	return routes
}

// WithRoutes applies the provider routes to the verdict opencode's stored and
// environment credentials reached: a definite verdict stands, otherwise the
// routes decide.
func WithRoutes(ctx context.Context, existing ports.AgentAuthStatus, routes []domain.AgentModelRoute) ports.AgentAuthStatus {
	return modelroute.Decide(ctx, existing, routes)
}
