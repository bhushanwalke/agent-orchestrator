package opencodev2

import (
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/modelroute"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencode"
)

type noNetwork struct{}

func (noNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("no network in tests")
}

// TestMain keeps route checks away from the developer's opencode config and
// the network.
func TestMain(m *testing.M) {
	emptyConfig, err := os.MkdirTemp("", "opencode-config-")
	if err != nil {
		panic(err)
	}
	opencode.ConfigHome = func() string { return emptyConfig }
	opencode.ManagedConfigDirs = nil
	modelroute.Client = &http.Client{Transport: noNetwork{}}
	code := m.Run()
	_ = os.RemoveAll(emptyConfig)
	os.Exit(code)
}
