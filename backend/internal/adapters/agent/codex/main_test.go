package codex

import (
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/modelroute"
)

type noNetwork struct{}

func (noNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("no network in tests")
}

// TestMain keeps route checks off the network: a config.toml fixture that
// names a base URL must never reach whatever listens there on this machine.
// Tests that check a route point modelroute.Client at their own server.
func TestMain(m *testing.M) {
	modelroute.Client = &http.Client{Transport: noNetwork{}}
	os.Exit(m.Run())
}
