package domain

import (
	"strings"
	"testing"
)

func TestRouteHostKeepsOnlyHostAndPort(t *testing.T) {
	cases := map[string]string{
		"https://gw.example.com/v1":                      "gw.example.com",
		"https://user:secret@gw.example.com:8443/v1?k=1": "gw.example.com:8443",
		"http://localhost:11434/v1#frag":                 "localhost:11434",
		"https://[::1]:8080/":                            "[::1]:8080",
		"  https://GW.Example.com  ":                     "gw.example.com",
		"ftp://gw.example.com":                           "",
		"gw.example.com/v1":                              "",
		"https://":                                       "",
		"not a url":                                      "",
		"":                                               "",
	}
	for raw, want := range cases {
		if got := RouteHost(raw); got != want {
			t.Errorf("RouteHost(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestIsLoopbackHost(t *testing.T) {
	for _, host := range []string{"localhost", "localhost:11434", "127.0.0.1", "127.8.9.1:80", "[::1]:8080", "::1"} {
		if !IsLoopbackHost(host) {
			t.Errorf("IsLoopbackHost(%q) = false", host)
		}
	}
	for _, host := range []string{"", "gw.example.com", "10.0.0.1", "localhost.example.com", "128.0.0.1"} {
		if IsLoopbackHost(host) {
			t.Errorf("IsLoopbackHost(%q) = true", host)
		}
	}
}

func TestAgentModelRouteSanitized(t *testing.T) {
	route, ok := AgentModelRoute{
		Name:             "  fp-gw ",
		Kind:             AgentModelRouteGateway,
		APIFamily:        "made-up",
		Host:             "gw.example.com",
		Credential:       "weird",
		CredentialSource: "GW_KEY",
		ConfigSource:     "bad\x00value",
		Verification:     "strange",
		IsDefault:        true,
	}.Sanitized()
	if !ok {
		t.Fatal("route with a known kind was dropped")
	}
	if route.Name != "fp-gw" || route.APIFamily != AgentModelRouteFamilyOther || route.Credential != AgentModelRouteCredentialUnknown ||
		route.Verification != AgentModelRouteNotAttempted || route.ConfigSource != "" || route.CredentialSource != "GW_KEY" || !route.IsDefault {
		t.Fatalf("sanitized = %+v", route)
	}
	for _, host := range []string{"gw.example.com/v1", "user@gw.example.com", "gw.example.com?x", "gw example", "gw#x"} {
		got, _ := AgentModelRoute{Kind: AgentModelRouteGateway, Host: host}.Sanitized()
		if got.Host != "" {
			t.Errorf("host %q kept as %q", host, got.Host)
		}
	}
	if _, ok := (AgentModelRoute{Kind: "other"}).Sanitized(); ok {
		t.Fatal("unknown kind kept")
	}
	if got, _ := (AgentModelRoute{Kind: AgentModelRouteCloud, Name: strings.Repeat("x", 300)}).Sanitized(); got.Name != "" {
		t.Fatal("overlong name kept")
	}
}

func TestSanitizedRoutesBoundsTheList(t *testing.T) {
	routes := make([]AgentModelRoute, 0, 20)
	for range 20 {
		routes = append(routes, AgentModelRoute{Kind: AgentModelRouteGateway})
	}
	routes = append([]AgentModelRoute{{Kind: "bogus"}}, routes...)
	got := SanitizedRoutes(routes)
	if len(got) != MaxAgentModelRoutes {
		t.Fatalf("len = %d, want %d", len(got), MaxAgentModelRoutes)
	}
	if SanitizedRoutes(nil) != nil {
		t.Fatal("no routes should stay nil")
	}
}

func TestVerificationDiagnosticCodes(t *testing.T) {
	cases := map[AgentModelRouteVerification]string{
		AgentModelRouteVerified:        "route_verified",
		AgentModelRouteRejected:        "route_rejected",
		AgentModelRouteUnverifiable:    "route_unverifiable",
		AgentModelRouteInconclusive:    "route_inconclusive",
		AgentModelRouteAccessChallenge: "route_access_challenge",
		AgentModelRouteUnreachable:     "route_unreachable",
		AgentModelRouteDisabled:        "route_probe_disabled",
		AgentModelRouteNotAttempted:    "route_identified",
	}
	for verification, code := range cases {
		if got := verification.DiagnosticCode(); got != code {
			t.Errorf("%s → %q, want %q", verification, got, code)
		}
		if d := NewAgentAuthDiagnostic("model_route", code); strings.HasPrefix(d.Reason, "The provider response did not establish") {
			t.Errorf("%s has no authored reason", code)
		}
	}
	if d := NewAgentAuthDiagnostic("model_route", "route_credential_missing"); d.NextStep == "" {
		t.Error("route_credential_missing has no next step")
	}
}
