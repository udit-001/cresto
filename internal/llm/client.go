package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	ErrEmptyResponse       = errors.New("llm: empty response from model")
	ErrMalformedJSON       = errors.New("llm: model returned malformed JSON")
	ErrNoModel             = errors.New("llm: model not installed on server")
	ErrServerStartTimeout  = errors.New("llm: LM Studio server did not come up in time")
	ErrServerStartFailed   = errors.New("llm: failed to start LM Studio server")
)

const (
	maxRetries             = 3
	defaultIdleTimeout     = 5 * time.Minute
	serverStartPollEvery   = 500 * time.Millisecond
	serverStartTimeout     = 60 * time.Second
)

// Client state values. Reported via Health() and consumed by the upload
// page's status pill. Transitional states (server_starting, model_loading,
// unloading) are short-lived; the rest reflect LM Studio's actual condition.
const (
	stateServerDown     = "server_down"
	stateServerStarting = "server_starting"
	stateNotInstalled   = "not_installed"
	stateNotLoaded      = "not_loaded"
	stateModelLoading   = "model_loading"
	stateLoaded         = "loaded"
	stateUnloading      = "unloading"
)

// serverStarter starts the LM Studio HTTP server. The default impl shells
// out to `lms server start`; tests inject a fake. Returning nil means the
// starter did its job — the caller polls Health until the server answers.
type serverStarter func(ctx context.Context) error

// Option configures a Client at construction.
type Option func(*Client)

// WithServerStarter replaces the default `lms server start` shell-out.
// Use in tests to avoid the real binary; the starter is the only true
// external dependency and the natural seam for state-machine tests.
func WithServerStarter(f func(context.Context) error) Option {
	return func(c *Client) { c.starter = f }
}

// WithIdleTimeout sets how long the model stays loaded after the last
// Extract before the idle loop unloads it. Default 5 min; tests use ~100ms.
func WithIdleTimeout(d time.Duration) Option {
	return func(c *Client) { c.idleTimeout = d }
}

type Client struct {
	baseURL string
	model   string
	http    *http.Client

	// Lifecycle state — all guarded by mu.
	mu          sync.Mutex
	state       string
	instanceID  string
	lastUsed    time.Time
	idleTimeout time.Duration
	starter     serverStarter
	stopCh      chan struct{}
	started     bool
}

func NewClient(baseURL, model string, opts ...Option) *Client {
	c := &Client{
		baseURL:     baseURL,
		model:       model,
		http:        &http.Client{Timeout: 2 * time.Minute},
		state:       stateServerDown,
		idleTimeout: defaultIdleTimeout,
		starter:     defaultServerStarter,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Start launches the idle-unload timer. Call once after wiring the server;
// the timer unloads the model when no Extract has run for idleTimeout.
// Calling Start twice is a no-op. Always pair with Stop.
func (c *Client) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return
	}
	c.stopCh = make(chan struct{})
	c.started = true
	go c.idleLoop(c.stopCh)
}

// Stop tears down the idle timer and best-effort unloads the model.
// Safe to call on a Client that was never Start'ed. After Stop, Extract
// still works (it auto-ensures readiness) but no further idle unload runs.
func (c *Client) Stop() {
	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		return
	}
	close(c.stopCh)
	c.started = false
	instanceID := c.instanceID
	loaded := c.state == stateLoaded
	c.mu.Unlock()

	if loaded && instanceID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.unloadModel(ctx, instanceID)
	}
}

func (c *Client) idleLoop(stopCh chan struct{}) {
	interval := c.idleTimeout / 2
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			c.maybeIdleUnload()
		}
	}
}

func (c *Client) maybeIdleUnload() {
	c.mu.Lock()
	if c.state != stateLoaded || time.Since(c.lastUsed) < c.idleTimeout {
		c.mu.Unlock()
		return
	}
	instanceID := c.instanceID
	c.state = stateUnloading
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := c.unloadModel(ctx, instanceID)

	c.mu.Lock()
	if err == nil {
		c.state = stateNotLoaded
		c.instanceID = ""
	} else {
		c.state = stateLoaded
	}
	c.mu.Unlock()
}

// Health reports the current state of the LM Studio server and the
// configured model. Returns one of:
//   - "loaded": model in RAM and ready to extract
//   - "not_loaded": model downloaded but not in RAM
//   - "not_installed": model ID not in LM Studio's catalog
//   - "server_down": LM Studio unreachable
//   - "server_starting" / "model_loading" / "unloading": mid-transition
//
// During a transition (Extract auto-starting the server or loading the
// model), Health returns the transitional state without re-probing, so
// callers see honest "loading…" feedback instead of a stale snapshot.
func (c *Client) Health() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.state {
	case stateServerStarting, stateModelLoading, stateUnloading:
		return c.state
	}
	fresh := c.probeState()
	c.state = fresh
	return fresh
}

// probeState asks LM Studio for the model's current state. Does not take
// the mutex — caller must hold c.mu. Returns one of the non-transitional
// state constants (server_down / not_installed / not_loaded / loaded).
func (c *Client) probeState() string {
	client := &http.Client{Timeout: 2 * time.Second}
	v0Base := strings.TrimSuffix(c.baseURL, "/v1") + "/api/v0"
	reqURL := v0Base + "/models/" + url.PathEscape(c.model)
	resp, err := client.Get(reqURL)
	if err != nil {
		return stateServerDown
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return stateNotInstalled
	case resp.StatusCode >= 400:
		return stateServerDown
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return stateServerDown
	}
	var info struct {
		State string `json:"state"`
	}
	if json.Unmarshal(body, &info) != nil {
		return stateServerDown
	}
	if info.State == "loaded" {
		return stateLoaded
	}
	return stateNotLoaded
}

// ensureReadyLocked transitions the client into the loaded state.
// Caller must hold c.mu. Handles server-start (via the injected starter),
// model-load (via /api/v1/models/load), and the polling between them.
func (c *Client) ensureReadyLocked(ctx context.Context) error {
	// Probe once to see where we are.
	c.state = c.probeState()
	switch c.state {
	case stateLoaded:
		c.lastUsed = time.Now()
		return nil
	case stateNotInstalled:
		return fmt.Errorf("%w: %s", ErrNoModel, c.model)
	case stateServerDown:
		if err := c.startServerLocked(ctx); err != nil {
			return err
		}
		// After start, fall through to load if model is now not_loaded.
		if c.state == stateLoaded {
			c.lastUsed = time.Now()
			return nil
		}
		if c.state == stateNotInstalled {
			return fmt.Errorf("%w: %s", ErrNoModel, c.model)
		}
		if c.state == stateServerDown {
			return ErrServerStartTimeout
		}
		fallthrough
	case stateNotLoaded:
		return c.loadModelLocked(ctx)
	default:
		return nil
	}
}

// startServerLocked runs the starter, then polls probeState until the
// server answers (or serverStartTimeout elapses). Sets c.state to the
// final probed state. Caller must hold c.mu.
func (c *Client) startServerLocked(ctx context.Context) error {
	c.state = stateServerStarting
	if err := c.starter(ctx); err != nil {
		c.state = stateServerDown
		return fmt.Errorf("%w: %v", ErrServerStartFailed, err)
	}
	deadline := time.Now().Add(serverStartTimeout)
	for time.Now().Before(deadline) {
		c.state = c.probeState()
		if c.state != stateServerDown {
			return nil
		}
		// Release the mutex during the sleep so Health() can still report.
		c.mu.Unlock()
		select {
		case <-time.After(serverStartPollEvery):
		case <-ctx.Done():
			c.mu.Lock()
			c.state = stateServerDown
			return ctx.Err()
		}
		c.mu.Lock()
	}
	c.state = stateServerDown
	return ErrServerStartTimeout
}

// loadModelLocked calls /api/v1/models/load and transitions to loaded.
// Caller must hold c.mu.
func (c *Client) loadModelLocked(ctx context.Context) error {
	c.state = stateModelLoading
	c.mu.Unlock()
	instanceID, err := c.loadModelHTTPRequest(ctx)
	c.mu.Lock()
	if err != nil {
		c.state = stateNotLoaded
		return fmt.Errorf("load model: %w", err)
	}
	c.instanceID = instanceID
	c.state = stateLoaded
	c.lastUsed = time.Now()
	return nil
}

// loadModelHTTPRequest performs the POST /api/v1/models/load request.
// Returns the instance_id (needed for later unload). Does not touch c.mu.
func (c *Client) loadModelHTTPRequest(ctx context.Context) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model": c.model,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.v1Base()+"/models/load", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var info struct {
		InstanceID string `json:"instance_id"`
		Status     string `json:"status"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return "", fmt.Errorf("parse load response: %w", err)
	}
	if info.Status != "loaded" {
		return "", fmt.Errorf("load status %q", info.Status)
	}
	return info.InstanceID, nil
}

// unloadModel performs POST /api/v1/models/unload. Does not touch c.mu.
func (c *Client) unloadModel(ctx context.Context, instanceID string) error {
	body, _ := json.Marshal(map[string]any{"instance_id": instanceID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.v1Base()+"/models/unload", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	return nil
}

func (c *Client) v1Base() string {
	return strings.TrimSuffix(c.baseURL, "/v1") + "/api/v1"
}

// defaultServerStarter shells out to `lms server start` detached.
// If `lms` isn't on PATH the error surfaces from exec.LookPath; callers
// should give the user a clear "install lms" message in that case.
func defaultServerStarter(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "lms", "server", "start")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	// Detach into its own session/process group so the server outlives
	// the parent process — `lms server start` blocks in the foreground,
	// so we Start (not Run) and let it run independently.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// Extract auto-ensures the LM Studio server is up and the configured model
// is loaded into RAM, then runs the extraction. If the server is down it
// shells out to `lms server start`; if the model is not loaded it calls
// /api/v1/models/load. Both can take tens of seconds on a cold start.
//
// All lifecycle state is owned by the Client; callers don't need to know
// whether the server was already up. After Extract the idle timer (if
// Start'd) will unload the model after idleTimeout of inactivity.
func (c *Client) Extract(image []byte) (*Extraction, error) {
	c.mu.Lock()
	if err := c.ensureReadyLocked(context.Background()); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	c.lastUsed = time.Now()
	c.mu.Unlock()

	imgB64 := base64.StdEncoding.EncodeToString(image)
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		ext, err := c.attempt(imgB64)
		if err == nil {
			return ext, nil
		}
		lastErr = err
		if !isRetryable(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("after %d attempts: %w", maxRetries, lastErr)
}

// Classify runs stage 2 of the pipeline: text-only canonical classification.
// It takes the raw extraction (from Extract) and the canonical vocabulary,
// and returns a parallel array of slugs — one per component. Shares the same
// server lifecycle, retry logic, and HTTP plumbing as Extract. On failure the
// caller falls back to the keyword mapper in web.MapExtraction.
func (c *Client) Classify(ext *Extraction, canonicals []CanonicalRef) (*Classification, error) {
	input := buildClassifyInput(ext, canonicals)
	inputJSON, _ := json.Marshal(input)

	c.mu.Lock()
	if err := c.ensureReadyLocked(context.Background()); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	c.lastUsed = time.Now()
	c.mu.Unlock()

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		class, err := c.attemptClassify(string(inputJSON))
		if err == nil {
			return class, nil
		}
		lastErr = err
		if !isRetryable(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("after %d attempts: %w", maxRetries, lastErr)
}

func isRetryable(err error) bool {
	return errors.Is(err, ErrEmptyResponse) ||
		errors.Is(err, ErrMalformedJSON) ||
		errors.Is(err, io.ErrUnexpectedEOF)
}

func (c *Client) attempt(imgB64 string) (*Extraction, error) {
	raw, err := c.chatCompletion(buildRequest(c.model, imgB64))
	if err != nil {
		return nil, err
	}
	return parseResponse(raw)
}

func (c *Client) attemptClassify(inputJSON string) (*Classification, error) {
	raw, err := c.chatCompletion(buildClassifyRequest(c.model, inputJSON))
	if err != nil {
		return nil, err
	}
	return parseClassifyResponse(raw)
}

// chatCompletion POSTs a chat completion request to LM Studio and returns the
// raw response body. Shared by Extract (vision) and Classify (text-only) so the
// HTTP plumbing — request building, status-code handling, body reading — has one
// source of truth. Callers parse the returned body into their own type.
func (c *Client) chatCompletion(reqBody []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNoModel
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	return raw, nil
}

func parseResponse(body []byte) (*Extraction, error) {
	content, err := extractContent(body)
	if err != nil {
		return nil, err
	}
	var ext Extraction
	if err := json.Unmarshal([]byte(content), &ext); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedJSON, err)
	}
	return &ext, nil
}

// extractContent pulls the text content from the first choice of an OpenAI-style
// chat completion response, strips markdown fences, and returns it. Shared by
// all chat-completion parsers (Extract, Classify) so the outer-envelope handling
// has one source of truth.
func extractContent(body []byte) (string, error) {
	var outer struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &outer); err != nil {
		return "", fmt.Errorf("parse outer response: %w", err)
	}
	if len(outer.Choices) == 0 {
		return "", ErrEmptyResponse
	}
	content := strings.TrimSpace(outer.Choices[0].Message.Content)
	if content == "" {
		return "", ErrEmptyResponse
	}
	return stripMarkdownFences(content), nil
}

func parseClassifyResponse(body []byte) (*Classification, error) {
	content, err := extractContent(body)
	if err != nil {
		return nil, err
	}
	var class Classification
	if err := json.Unmarshal([]byte(content), &class); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedJSON, err)
	}
	return &class, nil
}

func stripMarkdownFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) < 2 {
		return s
	}
	lines = lines[1:]
	if last := lines[len(lines)-1]; strings.HasPrefix(last, "```") {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func buildRequest(model, imgB64 string) []byte {
	type contentPart struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		ImageURL *struct {
			URL string `json:"url"`
		} `json:"image_url,omitempty"`
	}
	type message struct {
		Role    string        `json:"role"`
		Content []contentPart `json:"content"`
	}
	type request struct {
		Model    string    `json:"model"`
		Messages []message `json:"messages"`
	}
	req := request{
		Model: model,
		Messages: []message{
			{Role: "system", Content: []contentPart{{Type: "text", Text: systemPrompt}}},
			{Role: "user", Content: []contentPart{
				{Type: "text", Text: "Extract all financial data from this payslip image as JSON."},
				{Type: "image_url", ImageURL: &struct {
					URL string `json:"url"`
				}{URL: "data:image/png;base64," + imgB64}},
			}},
		},
	}
	b, _ := json.Marshal(req)
	return b
}

// buildClassifyInput assembles the JSON payload for stage 2: the extracted
// components plus the canonical vocabulary grouped by category. Grouping
// removes ambiguity for same-display-name canonicals in different categories
// (e.g. term_insurance_earning vs term_insurance_deduction) — the model picks
// from the matching list.
func buildClassifyInput(ext *Extraction, canonicals []CanonicalRef) classifyInput {
	var input classifyInput
	input.Earnings = ext.Earnings
	input.Deductions = ext.Deductions
	for _, c := range canonicals {
		if c.Category == "earning" {
			input.Canonicals.Earnings = append(input.Canonicals.Earnings, c)
		} else {
			input.Canonicals.Deductions = append(input.Canonicals.Deductions, c)
		}
	}
	return input
}

// buildClassifyRequest builds a text-only chat completion request (no image)
// for the classifier model. Uses the simple string content format since there
// are no image parts.
func buildClassifyRequest(model, inputJSON string) []byte {
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type request struct {
		Model    string    `json:"model"`
		Messages []message `json:"messages"`
	}
	req := request{
		Model: model,
		Messages: []message{
			{Role: "system", Content: classifyPrompt},
			{Role: "user", Content: inputJSON},
		},
	}
	b, _ := json.Marshal(req)
	return b
}
