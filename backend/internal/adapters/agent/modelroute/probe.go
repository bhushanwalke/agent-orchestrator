// Package modelroute checks the model routes harnesses are configured to use
// when they are not the provider's own login: a gateway, a cloud provider or a
// local endpoint. The only check is listing models, sent to the route's own
// base URL with exactly the credential the harness sends there, so it spends
// no tokens and has no side effects.
package modelroute

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Request is one route to check. Headers are the credential headers the
// harness sends on this route and nothing else; empty for a keyless route.
type Request struct {
	BaseURL string
	Family  domain.AgentModelRouteFamily
	Headers http.Header
}

// Result is what a check found.
type Result struct {
	Verification domain.AgentModelRouteVerification
	HTTPStatus   int
	Cached       bool
	CheckedAt    time.Time
	Duration     time.Duration
}

// Client sends the checks. Tests replace its transport; Check never follows
// a redirect whatever the client says.
var Client = &http.Client{}

var probeTimeout = 3 * time.Second

const (
	maxBody          = 1 << 20
	cacheTTL         = 5 * time.Minute
	anthropicVersion = "2023-06-01"
)

type cacheEntry struct {
	result  Result
	expires time.Time
}

var cache = struct {
	sync.Mutex
	entries map[string]cacheEntry
}{entries: map[string]cacheEntry{}}

// InvalidateCache drops every remembered check, for "Check again" and after a
// session's credential was rejected.
func InvalidateCache() {
	cache.Lock()
	cache.entries = map[string]cacheEntry{}
	cache.Unlock()
}

// Disabled reports whether AO_MODEL_ROUTE_PROBE=off turns route checks off.
// It is read at check time.
func Disabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("AO_MODEL_ROUTE_PROBE")), "off")
}

// ListURL is the model-listing URL for a route, or "" when the family has no
// side-effect-free listing AO can call.
func ListURL(baseURL string, family domain.AgentModelRouteFamily) string {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Hostname() == "" {
		return ""
	}
	path := strings.TrimRight(base.Path, "/")
	switch family {
	case domain.AgentModelRouteFamilyOpenAI, domain.AgentModelRouteFamilyOpenAICompatible:
		path += "/models"
	case domain.AgentModelRouteFamilyAnthropic:
		if strings.HasSuffix(path, "/v1") {
			path += "/models"
		} else {
			path += "/v1/models"
		}
	default:
		return ""
	}
	base.Path, base.RawPath, base.Fragment = path, "", ""
	return base.String()
}

// Check lists the route's models once, under every safety rule: GET only,
// redirects never followed, a short timeout within ctx's deadline, the body
// read capped and never kept, and no key sent in cleartext off this computer.
func Check(ctx context.Context, req Request) Result {
	started := time.Now()
	done := func(v domain.AgentModelRouteVerification, status int) Result {
		return Result{Verification: v, HTTPStatus: status, CheckedAt: time.Now(), Duration: time.Since(started)}
	}
	if Disabled() {
		return done(domain.AgentModelRouteDisabled, 0)
	}
	target := ListURL(req.BaseURL, req.Family)
	if target == "" {
		return done(domain.AgentModelRouteUnverifiable, 0)
	}
	parsed, _ := url.Parse(target)
	// Credentials in the URL itself would be sent too, and could not be
	// held back for the public-listing comparison; AO does not check those.
	if parsed.User != nil {
		return done(domain.AgentModelRouteInconclusive, 0)
	}
	keyed := len(req.Headers) > 0
	if keyed && parsed.Scheme == "http" && !domain.IsLoopbackHost(parsed.Host) {
		return done(domain.AgentModelRouteInconclusive, 0)
	}
	key := cacheKey(target, req.Headers)
	cache.Lock()
	entry, hit := cache.entries[key]
	cache.Unlock()
	if hit && time.Now().Before(entry.expires) {
		result := entry.result
		result.Cached = true
		return result
	}

	verification, status := list(ctx, target, req.Family, req.Headers)
	// A listing can be public; then it proves nothing about the key.
	if verification == domain.AgentModelRouteVerified && keyed {
		if anonymous, _ := list(ctx, target, req.Family, nil); anonymous == domain.AgentModelRouteVerified {
			verification = domain.AgentModelRouteInconclusive
		}
	}
	result := done(verification, status)
	if ctx.Err() == nil {
		cache.Lock()
		cache.entries[key] = cacheEntry{result: result, expires: time.Now().Add(cacheTTL)}
		cache.Unlock()
	}
	return result
}

func list(ctx context.Context, target string, family domain.AgentModelRouteFamily, headers http.Header) (domain.AgentModelRouteVerification, int) {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return domain.AgentModelRouteInconclusive, 0
	}
	for name, values := range headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	req.Header.Set("Accept", "application/json")
	if family == domain.AgentModelRouteFamilyAnthropic {
		req.Header.Set("anthropic-version", anthropicVersion)
	}
	client := &http.Client{Transport: Client.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return domain.AgentModelRouteUnreachable, 0
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		return domain.AgentModelRouteUnreachable, response.StatusCode
	}
	if len(body) > maxBody {
		body = nil
	}
	return classify(response.StatusCode, response.Header.Get("Content-Type"), body), response.StatusCode
}

// classify maps a listing response to a verification. A gateway that turns
// away unknown traffic answers with a redirect or a sign-in page, not JSON.
func classify(status int, contentType string, body []byte) domain.AgentModelRouteVerification {
	var object map[string]json.RawMessage
	isJSON := json.Unmarshal(body, &object) == nil
	html := strings.Contains(strings.ToLower(contentType), "text/html") || bytes.HasPrefix(bytes.TrimSpace(body), []byte("<"))
	switch {
	case status >= 300 && status < 400:
		return domain.AgentModelRouteAccessChallenge
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		if isJSON {
			return domain.AgentModelRouteRejected
		}
		return domain.AgentModelRouteAccessChallenge
	case status >= 200 && status < 300:
		if isJSON && (isArray(object["data"]) || isArray(object["models"])) {
			return domain.AgentModelRouteVerified
		}
		if html {
			return domain.AgentModelRouteAccessChallenge
		}
	}
	return domain.AgentModelRouteInconclusive
}

func isArray(raw json.RawMessage) bool {
	var items []json.RawMessage
	return len(raw) > 0 && json.Unmarshal(raw, &items) == nil && items != nil
}

func cacheKey(target string, headers http.Header) string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, http.CanonicalHeaderKey(name))
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		hash.Write([]byte(name + "\x00" + strings.Join(headers.Values(name), "\x00") + "\x00"))
	}
	return target + "\x00" + hex.EncodeToString(hash.Sum(nil))
}

// Record sets the check's outcome on route, records the route on the auth
// evidence in ctx with a model_route diagnostic, and returns the route. The
// diagnostic names the route by id and host, never by a credential.
func Record(ctx context.Context, route domain.AgentModelRoute, result Result) domain.AgentModelRoute {
	route.Verification, route.HTTPStatus = result.Verification, result.HTTPStatus
	ports.RecordModelRoute(ctx, route)
	step := domain.NewAgentAuthDiagnostic("model_route", result.Verification.DiagnosticCode())
	step.Source = source(route)
	step.HTTPStatus, step.Cached = result.HTTPStatus, result.Cached
	if !result.CheckedAt.IsZero() {
		step.CheckedAt = result.CheckedAt
	}
	step.DurationMs = result.Duration.Milliseconds()
	ports.RecordAuthDiagnostic(ctx, step)
	return route
}

// RecordMissingCredential records a route whose credential variable is unset,
// so it was not checked.
func RecordMissingCredential(ctx context.Context, route domain.AgentModelRoute) domain.AgentModelRoute {
	route.Verification = domain.AgentModelRouteNotAttempted
	ports.RecordModelRoute(ctx, route)
	step := domain.NewAgentAuthDiagnostic("model_route", "route_credential_missing")
	step.Source = source(route)
	ports.RecordAuthDiagnostic(ctx, step)
	return route
}

func source(route domain.AgentModelRoute) string {
	parts := make([]string, 0, 2)
	for _, part := range []string{route.Name, route.Host} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, " · ")
}

// Kind is local for a loopback host and gateway otherwise.
func Kind(host string) domain.AgentModelRouteKind {
	if domain.IsLoopbackHost(host) {
		return domain.AgentModelRouteLocal
	}
	return domain.AgentModelRouteGateway
}

// Status aggregates routes: any verified route is authorized, routes that are
// all rejected are unauthorized, anything else identified is configured. ok
// is false when there are no routes.
func Status(routes []domain.AgentModelRoute) (ports.AgentAuthStatus, bool) {
	if len(routes) == 0 {
		return ports.AgentAuthStatusUnknown, false
	}
	rejected := 0
	for _, route := range routes {
		switch route.Verification {
		case domain.AgentModelRouteVerified:
			return ports.AgentAuthStatusAuthorized, true
		case domain.AgentModelRouteRejected:
			rejected++
		}
	}
	if rejected == len(routes) {
		return ports.AgentAuthStatusUnauthorized, true
	}
	return ports.AgentAuthStatusConfigured, true
}

var envVarName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// Decide applies routes on top of the verdict the harness's existing rules
// reached: a definite verdict stands, otherwise the routes decide. When a
// verified route authorizes, it becomes the recorded account evidence.
func Decide(ctx context.Context, existing ports.AgentAuthStatus, routes []domain.AgentModelRoute) ports.AgentAuthStatus {
	if existing == ports.AgentAuthStatusAuthorized || existing == ports.AgentAuthStatusUnauthorized {
		return existing
	}
	status, ok := Status(routes)
	if !ok {
		return existing
	}
	if status == ports.AgentAuthStatusAuthorized {
		for _, route := range routes {
			if route.Verification == domain.AgentModelRouteVerified {
				ports.RecordAuthEvidence(ctx, routeEvidence(route))
				break
			}
		}
	}
	return status
}

func routeEvidence(route domain.AgentModelRoute) domain.AgentAccountIdentity {
	identity := domain.AgentAccountIdentity{Provider: route.Name}
	switch route.Credential {
	case domain.AgentModelRouteCredentialAPIKey:
		identity.Method = domain.AgentAccountMethodAPIKey
	case domain.AgentModelRouteCredentialToken:
		identity.Method = domain.AgentAccountMethodToken
	}
	switch {
	case route.CredentialSource == "":
	case envVarName.MatchString(route.CredentialSource):
		identity.SourceKind, identity.Source = domain.AgentCredentialSourceEnvironment, route.CredentialSource
	default:
		identity.SourceKind, identity.Source = domain.AgentCredentialSourceFile, route.CredentialSource
	}
	return identity
}
