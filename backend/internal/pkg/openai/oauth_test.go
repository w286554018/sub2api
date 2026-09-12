package openai

import (
	"net/url"
	"sync"
	"testing"
	"time"
)

func TestSessionStore_Stop_Idempotent(t *testing.T) {
	store := NewSessionStore()

	store.Stop()
	store.Stop()

	select {
	case <-store.stopCh:
		// ok
	case <-time.After(time.Second):
		t.Fatal("stopCh 未关闭")
	}
}

func TestSessionStore_Stop_Concurrent(t *testing.T) {
	store := NewSessionStore()

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store.Stop()
		}()
	}

	wg.Wait()

	select {
	case <-store.stopCh:
		// ok
	case <-time.After(time.Second):
		t.Fatal("stopCh 未关闭")
	}
}

func TestBuildAuthorizationURLForPlatform_OpenAI(t *testing.T) {
	authURL := BuildAuthorizationURLForPlatform("state-1", "challenge-1", DefaultRedirectURI, OAuthPlatformOpenAI, "")
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("Parse URL failed: %v", err)
	}
	q := parsed.Query()
	if got := q.Get("client_id"); got != ClientID {
		t.Fatalf("client_id mismatch: got=%q want=%q", got, ClientID)
	}
	if got := q.Get("codex_cli_simplified_flow"); got != "true" {
		t.Fatalf("codex flow mismatch: got=%q want=true", got)
	}
	if got := q.Get("id_token_add_organizations"); got != "true" {
		t.Fatalf("id_token_add_organizations mismatch: got=%q want=true", got)
	}
	if got := q.Get("originator"); got != "codex-tui" {
		t.Fatalf("originator mismatch: got=%q want=codex-tui", got)
	}
}

func TestBuildAuthorizationURLIdentityEncoding(t *testing.T) {
	tests := []struct{ originator, want string }{
		{"", "codex-tui"},
		{" \t ", "codex-tui"},
		{" codex_cli_rs ", "codex_cli_rs"},
		{"client&scope=extra+value", "client&scope=extra+value"},
	}
	for _, tt := range tests {
		t.Run(tt.originator, func(t *testing.T) {
			const redirect = "http://localhost:1455/auth/callback?state=a+b&x=1"
			raw := BuildAuthorizationURLForPlatform("state&value", "challenge+value", redirect, OAuthPlatformOpenAI, tt.originator)
			parsed, err := url.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			q := parsed.Query()
			for key, want := range map[string]string{
				"originator": tt.want, "scope": "openid profile email offline_access",
				"state": "state&value", "code_challenge": "challenge+value",
				"redirect_uri": redirect, "code_challenge_method": "S256",
				"id_token_add_organizations": "true", "codex_cli_simplified_flow": "true",
			} {
				if got := q[key]; len(got) != 1 || got[0] != want {
					t.Errorf("%s did not round-trip: got=%q want=%q", key, got, want)
				}
			}
		})
	}
	parsed, err := url.Parse(BuildAuthorizationURL("state", "challenge", ""))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("originator") != "codex-tui" || parsed.Query().Get("redirect_uri") != DefaultRedirectURI {
		t.Fatal("default wrapper lost identity or redirect")
	}
}
