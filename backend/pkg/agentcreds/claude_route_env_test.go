package agentcreds

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveClaudeRouteEnvFallsBackToUserSettings(t *testing.T) {
	dir := t.TempDir()
	settings := `{"apiKeyHelper":"~/bin/key","env":{"CLAUDE_CODE_USE_BEDROCK":true,"CLAUDE_CODE_SKIP_BEDROCK_AUTH":1,"ANTHROPIC_BEDROCK_BASE_URL":"https://gw.example.com/bedrock","AWS_REGION":"us-east-1","AWS_BEARER_TOKEN_BEDROCK":"secret","ANTHROPIC_API_KEY":"ignored"}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"CLAUDE_CONFIG_DIR": dir, "AWS_REGION": "eu-west-1"}
	route := ResolveClaudeRouteEnv(context.Background(), ResolveOptions{
		CommandEnv: env,
		Env:        func(string) string { return "" },
	})
	if !route.Truthy("CLAUDE_CODE_USE_BEDROCK") || !route.FromSettings("CLAUDE_CODE_USE_BEDROCK") || !route.Truthy("CLAUDE_CODE_SKIP_BEDROCK_AUTH") {
		t.Fatalf("flags not read from settings: %+v", route)
	}
	if route.Get("AWS_REGION") != "eu-west-1" || route.FromSettings("AWS_REGION") {
		t.Fatalf("launch environment must win: %q", route.Get("AWS_REGION"))
	}
	if route.Get("AWS_BEARER_TOKEN_BEDROCK") != "set" {
		t.Fatalf("presence key kept its value: %q", route.Get("AWS_BEARER_TOKEN_BEDROCK"))
	}
	if route.Get("ANTHROPIC_API_KEY") != "" || !route.APIKeyHelper {
		t.Fatalf("route env = %+v", route)
	}
}
