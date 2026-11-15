package domain

import (
	"net"
	"net/url"
	"strings"
)

// AgentModelRouteKind is where a harness sends its model requests when that
// is not the provider's own first-party API.
type AgentModelRouteKind string

const (
	// AgentModelRouteFirstParty is the provider's own API.
	AgentModelRouteFirstParty AgentModelRouteKind = "first_party"
	// AgentModelRouteGateway is an explicit base URL that is not the
	// provider's own default: a proxy, a company gateway, a hosted router.
	AgentModelRouteGateway AgentModelRouteKind = "gateway"
	// AgentModelRouteCloud is Bedrock, Vertex or Foundry, selected by the
	// harness's own switch.
	AgentModelRouteCloud AgentModelRouteKind = "cloud"
	// AgentModelRouteLocal is an endpoint on this computer.
	AgentModelRouteLocal AgentModelRouteKind = "local"
)

// AgentModelRouteFamily is the API a route speaks.
type AgentModelRouteFamily string

// Route API families.
const (
	AgentModelRouteFamilyAnthropic        AgentModelRouteFamily = "anthropic"
	AgentModelRouteFamilyOpenAI           AgentModelRouteFamily = "openai"
	AgentModelRouteFamilyOpenAICompatible AgentModelRouteFamily = "openai_compatible"
	AgentModelRouteFamilyBedrock          AgentModelRouteFamily = "bedrock"
	AgentModelRouteFamilyVertex           AgentModelRouteFamily = "vertex"
	AgentModelRouteFamilyFoundry          AgentModelRouteFamily = "foundry"
	AgentModelRouteFamilyGoogle           AgentModelRouteFamily = "google"
	AgentModelRouteFamilyOther            AgentModelRouteFamily = "other"
)

// AgentModelRouteCredential is what the harness sends on a route. None means
// it is configured to send no key, so access is decided by the network or an
// ambient identity that AO cannot see.
type AgentModelRouteCredential string

// Route credential modes.
const (
	AgentModelRouteCredentialAccount          AgentModelRouteCredential = "account"
	AgentModelRouteCredentialAPIKey           AgentModelRouteCredential = "api_key"
	AgentModelRouteCredentialToken            AgentModelRouteCredential = "token"
	AgentModelRouteCredentialCloudCredentials AgentModelRouteCredential = "cloud_credentials" //nolint:gosec // G101: an enum value, not a credential
	AgentModelRouteCredentialNone             AgentModelRouteCredential = "none"
	AgentModelRouteCredentialUnknown          AgentModelRouteCredential = "unknown"
)

// AgentModelRouteVerification is what AO's side-effect-free check of a route
// found.
type AgentModelRouteVerification string

// Route verifications.
const (
	AgentModelRouteVerified        AgentModelRouteVerification = "verified"
	AgentModelRouteRejected        AgentModelRouteVerification = "rejected"
	AgentModelRouteUnverifiable    AgentModelRouteVerification = "unverifiable"
	AgentModelRouteInconclusive    AgentModelRouteVerification = "inconclusive"
	AgentModelRouteAccessChallenge AgentModelRouteVerification = "access_challenge"
	AgentModelRouteUnreachable     AgentModelRouteVerification = "unreachable"
	AgentModelRouteNotAttempted    AgentModelRouteVerification = "not_attempted"
	AgentModelRouteDisabled        AgentModelRouteVerification = "disabled"
)

// MaxAgentModelRoutes bounds the routes one observation carries.
const MaxAgentModelRoutes = 16

// AgentModelRoute is one route a harness is configured to send model
// requests over. Every field is display-only: Host is hostname[:port] and the
// sources are variable names or home-relative paths, never a value.
type AgentModelRoute struct {
	// Name is the harness's id for the route, such as a Codex model_provider
	// or an OpenCode provider id.
	Name      string                `json:"name,omitempty"`
	Kind      AgentModelRouteKind   `json:"kind" enum:"first_party,gateway,cloud,local"`
	APIFamily AgentModelRouteFamily `json:"apiFamily" enum:"anthropic,openai,openai_compatible,bedrock,vertex,foundry,google,other"`
	Host      string                `json:"host,omitempty"`
	// Credential is what the harness sends on this route.
	Credential AgentModelRouteCredential `json:"credential" enum:"account,api_key,token,cloud_credentials,none,unknown"`
	// CredentialSource names the variable or file the credential comes from.
	CredentialSource string `json:"credentialSource,omitempty"`
	// ConfigSource names the variable or file the route was configured in.
	ConfigSource string                      `json:"configSource,omitempty"`
	Verification AgentModelRouteVerification `json:"verification" enum:"verified,rejected,unverifiable,inconclusive,access_challenge,unreachable,not_attempted,disabled"`
	HTTPStatus   int                         `json:"httpStatus,omitempty"`
	// IsDefault marks the route the harness's default model uses, when known.
	IsDefault bool `json:"isDefault,omitempty"`
}

// RouteHost returns hostname[:port] of an absolute http or https URL, lower
// case, dropping scheme, userinfo, path, query and fragment. Anything else
// returns "".
func RouteHost(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return ""
	}
	return strings.ToLower(parsed.Host)
}

// IsLoopbackHost reports whether host (hostname[:port]) is this computer:
// localhost, 127.0.0.0/8 or ::1.
func IsLoopbackHost(host string) bool {
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	name = strings.TrimSuffix(strings.TrimPrefix(name, "["), "]")
	if strings.EqualFold(name, "localhost") {
		return true
	}
	ip := net.ParseIP(name)
	return ip != nil && ip.IsLoopback()
}

// Sanitized bounds every string like account identity fields, drops a host
// that is more than hostname[:port], and maps unknown enum values to their
// neutral value. ok is false for an unknown kind.
func (r AgentModelRoute) Sanitized() (AgentModelRoute, bool) {
	switch r.Kind {
	case AgentModelRouteFirstParty, AgentModelRouteGateway, AgentModelRouteCloud, AgentModelRouteLocal:
	default:
		return AgentModelRoute{}, false
	}
	clean := AgentModelRoute{
		Name:             displayAccountField(r.Name),
		Kind:             r.Kind,
		APIFamily:        AgentModelRouteFamilyOther,
		Host:             strings.ToLower(displayAccountField(r.Host)),
		Credential:       AgentModelRouteCredentialUnknown,
		CredentialSource: displayAccountField(r.CredentialSource),
		ConfigSource:     displayAccountField(r.ConfigSource),
		Verification:     AgentModelRouteNotAttempted,
		IsDefault:        r.IsDefault,
	}
	if strings.ContainsAny(clean.Host, "/?#@ ") {
		clean.Host = ""
	}
	switch r.APIFamily {
	case AgentModelRouteFamilyAnthropic, AgentModelRouteFamilyOpenAI, AgentModelRouteFamilyOpenAICompatible,
		AgentModelRouteFamilyBedrock, AgentModelRouteFamilyVertex, AgentModelRouteFamilyFoundry, AgentModelRouteFamilyGoogle:
		clean.APIFamily = r.APIFamily
	}
	switch r.Credential {
	case AgentModelRouteCredentialAccount, AgentModelRouteCredentialAPIKey, AgentModelRouteCredentialToken,
		AgentModelRouteCredentialCloudCredentials, AgentModelRouteCredentialNone:
		clean.Credential = r.Credential
	}
	switch r.Verification {
	case AgentModelRouteVerified, AgentModelRouteRejected, AgentModelRouteUnverifiable, AgentModelRouteInconclusive,
		AgentModelRouteAccessChallenge, AgentModelRouteUnreachable, AgentModelRouteDisabled:
		clean.Verification = r.Verification
	}
	if r.HTTPStatus >= 100 && r.HTTPStatus <= 599 {
		clean.HTTPStatus = r.HTTPStatus
	}
	return clean, true
}

// SanitizedRoutes sanitizes each route, drops unknown kinds and keeps at most
// MaxAgentModelRoutes.
func SanitizedRoutes(routes []AgentModelRoute) []AgentModelRoute {
	var out []AgentModelRoute
	for _, route := range routes {
		if len(out) == MaxAgentModelRoutes {
			break
		}
		if clean, ok := route.Sanitized(); ok {
			out = append(out, clean)
		}
	}
	return out
}

// DiagnosticCode is the route_* diagnostic code for a verification.
func (v AgentModelRouteVerification) DiagnosticCode() string {
	switch v {
	case AgentModelRouteVerified:
		return "route_verified"
	case AgentModelRouteRejected:
		return "route_rejected"
	case AgentModelRouteUnverifiable:
		return "route_unverifiable"
	case AgentModelRouteInconclusive:
		return "route_inconclusive"
	case AgentModelRouteAccessChallenge:
		return "route_access_challenge"
	case AgentModelRouteUnreachable:
		return "route_unreachable"
	case AgentModelRouteDisabled:
		return "route_probe_disabled"
	default:
		return "route_identified"
	}
}
