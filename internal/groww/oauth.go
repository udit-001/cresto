package groww

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// startOAuth implements the full authorization-code + PKCE flow:
//
//  1. DCR — POST to Groww's register endpoint. Groww ignores the body and
//     returns a shared production client (client_id + client_secret). The
//     secret rotates, so we fetch it fresh each time.
//  2. PKCE — generate a random verifier and its S256 challenge.
//  3. Transient listener — start an http.Server on localhost:52155 that
//     handles GET /oauth/callback. When Groww redirects back with the
//     authorization code, the listener exchanges it for an access token,
//     persists it, and redirects the browser to returnURL.
//  4. Return the authorize URL for the browser.
//
// The listener shuts down after one callback or after 5 minutes (whichever
// comes first). The goroutine is fire-and-forget — the caller has already
// redirected the user's browser by the time it runs.
func (c *Client) startOAuth(returnURL string) (string, error) {
	creds, err := c.registerClient()
	if err != nil {
		return "", fmt.Errorf("register client: %w", err)
	}

	verifier, challenge, err := generatePKCE()
	if err != nil {
		return "", fmt.Errorf("generate pkce: %w", err)
	}

	state, err := randomString(32)
	if err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}

	redirectURI := fmt.Sprintf("http://localhost:%d/oauth/callback", c.callbackPort)

	// Start the transient listener before building the URL so the port is
	// ready when the browser hits Groww's redirect. If the port is busy,
	// this fails fast with a clear error.
	cb := &callbackServer{
		tokenURL:     c.tokenURL,
		redirectURI:  redirectURI,
		creds:        creds,
		verifier:     verifier,
		state:        state,
		returnURL:    returnURL,
		saveToken:    c.saveToken,
	}
	if err := cb.start(c.callbackPort); err != nil {
		return "", fmt.Errorf("start callback listener on port %d: %w (is another MCP client using this port?)", c.callbackPort, err)
	}

	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {creds.ClientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"resource":              {"https://mcp.groww.in/"},
	}
	authURL := c.authorizeURL + "?" + params.Encode()
	return authURL, nil
}

// clientCreds holds the DCR-returned client registration. Groww's DCR
// ignores the request body and returns a shared production client, so
// these fields are the same for every caller.
type clientCreds struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// registerClient calls Groww's DCR endpoint. Groww ignores the body and
// returns a fixed production client, but the secret rotates, so we call
// this fresh each auth flow.
func (c *Client) registerClient() (*clientCreds, error) {
	body := strings.NewReader(`{
		"client_name": "Cresto",
		"redirect_uris": ["http://localhost:52155/oauth/callback"],
		"grant_types": ["authorization_code"],
		"response_types": ["code"],
		"token_endpoint_auth_method": "client_secret_basic"
	}`)

	req, err := http.NewRequestWithContext(context.Background(), "POST", c.registerURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("dcr returned %d: %s", resp.StatusCode, string(raw))
	}

	var creds clientCreds
	if err := json.NewDecoder(resp.Body).Decode(&creds); err != nil {
		return nil, fmt.Errorf("decode dcr response: %w", err)
	}
	return &creds, nil
}

// generatePKCE creates a random code verifier (43–128 chars from the
// unreserved set) and its S256 code challenge (base64url without padding).
func generatePKCE() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// randomString returns a URL-safe random string of n bytes.
func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// callbackServer is the transient HTTP listener that receives the OAuth
// callback on localhost:52155. It handles one request, exchanges the code
// for a token, saves it, redirects the browser, and shuts down.
type callbackServer struct {
	tokenURL    string
	redirectURI string
	creds       *clientCreds
	verifier    string
	state       string
	returnURL   string
	saveToken   func(*tokenFile) error
	server      *http.Server
}

func (cb *callbackServer) start(port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /oauth/callback", cb.handle)
	cb.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}

	go func() {
		_ = cb.server.Serve(ln)
	}()

	// Auto-shutdown after 5 minutes regardless of whether a callback arrived.
	go func() {
		time.Sleep(5 * time.Minute)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = cb.server.Shutdown(ctx)
	}()

	return nil
}

func (cb *callbackServer) handle(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	errParam := r.URL.Query().Get("error")

	if errParam != "" {
		desc := r.URL.Query().Get("error_description")
		cb.fail(w, fmt.Sprintf("Groww denied access: %s (%s)", errParam, desc))
		return
	}
	if code == "" || state == "" {
		cb.fail(w, "Missing authorization code or state in callback.")
		return
	}
	if state != cb.state {
		cb.fail(w, "State mismatch — possible CSRF. Aborting.")
		return
	}

	tok, err := cb.exchangeCode(code)
	if err != nil {
		cb.fail(w, "Token exchange failed: "+err.Error())
		return
	}

	if err := cb.saveToken(tok); err != nil {
		cb.fail(w, "Could not save token: "+err.Error())
		return
	}

	// Success — redirect the browser back to Cresto.
	http.Redirect(w, r, cb.returnURL, http.StatusSeeOther)

	// Shut down the listener after sending the response.
	go func() {
		time.Sleep(500 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = cb.server.Shutdown(ctx)
	}()
}

// exchangeCode trades the authorization code for an access token.
func (cb *callbackServer) exchangeCode(code string) (*tokenFile, error) {
	form := url.Values{
		"grant_type":     {"authorization_code"},
		"code":           {code},
		"redirect_uri":   {cb.redirectURI},
		"code_verifier":  {cb.verifier},
		"resource":       {"https://mcp.groww.in/"},
	}

	req, err := http.NewRequest("POST", cb.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	// Groww's AS metadata advertises client_secret_basic as the only supported
	// auth method — credentials go in the Authorization header, not the body.
	req.SetBasicAuth(cb.creds.ClientID, cb.creds.ClientSecret)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(raw))
	}

	var tr struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}
	if tr.Error != "" {
		return nil, fmt.Errorf("token error: %s", tr.Error)
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("empty access token in response: %s", string(raw))
	}

	now := time.Now()
	expiresAt := now.Add(24 * time.Hour)
	if tr.ExpiresIn > 0 {
		expiresAt = now.Add(time.Duration(tr.ExpiresIn) * time.Second)
	}

	return &tokenFile{
		AccessToken: tr.AccessToken,
		TokenType:   tr.TokenType,
		ExpiresAt:   expiresAt,
		ObtainedAt:  now,
	}, nil
}

// fail renders a simple error page and shuts down the listener.
func (cb *callbackServer) fail(w http.ResponseWriter, msg string) {
	log.Printf("groww oauth: %s", msg)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, "<html><body><h2>Groww connection failed</h2><p>%s</p><p>You can close this tab.</p></body></html>", msg)

	go func() {
		time.Sleep(500 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = cb.server.Shutdown(ctx)
	}()
}
