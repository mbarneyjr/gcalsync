package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
)

type TokenStore struct {
	ConfigDir string
}

func (ts *TokenStore) TokenPath(account string) string {
	return filepath.Join(ts.ConfigDir, account+".token.json")
}

func (ts *TokenStore) Load(account string) (*oauth2.Token, error) {
	data, err := os.ReadFile(ts.TokenPath(account))
	if err != nil {
		return nil, err
	}
	var tok oauth2.Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, fmt.Errorf("parsing token for %s: %w", account, err)
	}
	return &tok, nil
}

func (ts *TokenStore) Save(account string, tok *oauth2.Token) error {
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ts.TokenPath(account), data, 0o600)
}

func (ts *TokenStore) Delete(account string) error {
	err := os.Remove(ts.TokenPath(account))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

type OAuthFlow struct {
	ClientID     string
	ClientSecret string
	Ports        []int
	TokenStore   *TokenStore
}

func (f *OAuthFlow) oauthConfig(port int) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     f.ClientID,
		ClientSecret: f.ClientSecret,
		Endpoint:     google.Endpoint,
		RedirectURL:  fmt.Sprintf("http://localhost:%d", port),
		Scopes:       []string{calendar.CalendarScope},
	}
}

func (f *OAuthFlow) Authenticate(account string) (*oauth2.Token, error) {
	for _, port := range f.Ports {
		tok, err := f.tryPort(port)
		if err != nil {
			continue
		}
		if err := f.TokenStore.Save(account, tok); err != nil {
			return nil, fmt.Errorf("saving token: %w", err)
		}
		return tok, nil
	}
	return nil, fmt.Errorf("could not start OAuth callback server on any port: %v", f.Ports)
}

func (f *OAuthFlow) tryPort(port int) (*oauth2.Token, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, err
	}
	defer listener.Close()

	cfg := f.oauthConfig(port)
	authURL := cfg.AuthCodeURL("state", oauth2.AccessTypeOffline, oauth2.ApprovalForce)

	copyToClipboard(authURL)
	fmt.Printf("Open this URL to authorize:\n%s\n", authURL)

	var (
		token   *oauth2.Token
		authErr error
		wg      sync.WaitGroup
	)
	wg.Add(1)

	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer wg.Done()
			code := r.URL.Query().Get("code")
			if code == "" {
				authErr = fmt.Errorf("no code in callback")
				fmt.Fprintln(w, "Authorization failed.")
				return
			}
			token, authErr = cfg.Exchange(context.Background(), code)
			if authErr != nil {
				fmt.Fprintln(w, "Authorization failed.")
				return
			}
			fmt.Fprintln(w, "Authorization successful! You can close this tab.")
		}),
	}

	go srv.Serve(listener)
	wg.Wait()
	srv.Close()

	return token, authErr
}

func (f *OAuthFlow) Client(ctx context.Context, account string) (*http.Client, error) {
	tok, err := f.TokenStore.Load(account)
	if err != nil {
		return nil, fmt.Errorf("loading token for %s: %w (run: gcalsync account refresh %s)", account, err, account)
	}

	cfg := f.oauthConfig(f.Ports[0])
	ts := &savingTokenSource{
		src:     cfg.TokenSource(ctx, tok),
		store:   f.TokenStore,
		account: account,
	}
	return oauth2.NewClient(ctx, ts), nil
}

// savingTokenSource wraps a token source and persists new tokens to disk.
type savingTokenSource struct {
	src     oauth2.TokenSource
	store   *TokenStore
	account string
	mu      sync.Mutex
}

func (s *savingTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tok, err := s.src.Token()
	if err != nil {
		return nil, err
	}

	// oauth2.ReuseTokenSource only calls the underlying source when expired,
	// so any token returned here is freshly refreshed — save it.
	if err := s.store.Save(s.account, tok); err != nil {
		// Non-fatal: log but don't fail the request
		fmt.Fprintf(os.Stderr, "warning: failed to save refreshed token for %s: %v\n", s.account, err)
	}
	return tok, nil
}

func copyToClipboard(text string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		cmd = exec.Command("xclip", "-selection", "clipboard")
	default:
		return
	}
	cmd.Stdin = nil
	pipe, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}
	pipe.Write([]byte(text))
	pipe.Close()
	cmd.Wait()
}
