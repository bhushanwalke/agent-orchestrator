package github

import "testing"

func TestParseGitHubRepoRejectsLocalPaths(t *testing.T) {
	for remote, want := range map[string]bool{
		"/tmp/ao-e2e-todo3.git":             false,
		"/tmp/a/b":                          false,
		`a\b`:                               false,
		"./a/b":                             false,
		"../a/b":                            false,
		"~/a/b":                             false,
		`C:\a\b`:                            false,
		"C:/a/b":                            false,
		"tmp/x.git":                         false,
		"file:///tmp/a/b.git":               false,
		"owner/repo":                        true,
		"git@github.com:owner/repo.git":     true,
		"https://github.com/owner/repo":     true,
		"https://github.com/owner/repo.git": true,
	} {
		if _, got := parseGitHubRepo(remote); got != want {
			t.Errorf("parseGitHubRepo(%q) ok = %v, want %v", remote, got, want)
		}
	}
	if repo, _ := parseGitHubRepo("owner/repo"); repo.Host != "github.com" || repo.Repo != "owner/repo" {
		t.Errorf("shorthand = %+v", repo)
	}
}
