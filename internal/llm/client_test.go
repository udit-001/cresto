package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func wrapContent(t *testing.T, inner interface{}) []byte {
	t.Helper()
	innerJSON, err := json.Marshal(inner)
	if err != nil {
		t.Fatalf("marshal inner: %v", err)
	}
	outer := map[string]interface{}{
		"choices": []map[string]interface{}{
			{"message": map[string]interface{}{"content": string(innerJSON)}},
		},
	}
	b, err := json.Marshal(outer)
	if err != nil {
		t.Fatalf("marshal outer: %v", err)
	}
	return b
}

func TestParseResponse_Valid(t *testing.T) {
	body := wrapContent(t, Extraction{
		Company:   "XYZ INFOTECH PRIVATE LIMITED",
		PayPeriod: "June 2026",
		Earnings:  []Component{{Label: "Basic", Amount: 112500, YTD: 337500}},
		Deductions: []Component{{Label: "PF", Amount: 13500, YTD: 40500}},
		Totals: Totals{Earnings: 112500, Deductions: 13500, NetPay: 99000,
			EarningsYTD: 337500, DeductionsYTD: 40500, NetPayYTD: 297000},
		Other: Other{EmployerPFContribution: 13500},
	})

	got, err := parseResponse(body)
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	if got.Company != "XYZ INFOTECH PRIVATE LIMITED" {
		t.Errorf("Company = %q", got.Company)
	}
	if len(got.Earnings) != 1 || got.Earnings[0].Label != "Basic" || got.Earnings[0].Amount != 112500 {
		t.Errorf("Earnings = %+v", got.Earnings)
	}
	if got.Totals.NetPay != 99000 {
		t.Errorf("Totals.NetPay = %v", got.Totals.NetPay)
	}
	if got.Other.EmployerPFContribution != 13500 {
		t.Errorf("Other.EmployerPFContribution = %v", got.Other.EmployerPFContribution)
	}
}

func TestParseResponse_ContentWithMarkdownFences(t *testing.T) {
	fence := "```"
	innerJSON := `{"company":"ACME","pay_period":"May 2026","earnings":[],"deductions":[],"totals":{"earnings":0,"deductions":0,"net_pay":0,"earnings_ytd":0,"deductions_ytd":0,"net_pay_ytd":0},"other":{}}`
	content := fence + "json\n" + innerJSON + "\n" + fence
	outer := map[string]interface{}{
		"choices": []map[string]interface{}{
			{"message": map[string]interface{}{"content": content}},
		},
	}
	body, _ := json.Marshal(outer)

	got, err := parseResponse(body)
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	if got.Company != "ACME" {
		t.Errorf("Company = %q", got.Company)
	}
}

func TestParseResponse_NoChoices(t *testing.T) {
	body := []byte(`{"choices": []}`)
	_, err := parseResponse(body)
	if !errors.Is(err, ErrEmptyResponse) {
		t.Errorf("err = %v, want ErrEmptyResponse", err)
	}
}

func TestParseResponse_MalformedInnerJSON(t *testing.T) {
	outer := map[string]interface{}{
		"choices": []map[string]interface{}{
			{"message": map[string]interface{}{"content": "not json"}},
		},
	}
	body, _ := json.Marshal(outer)
	_, err := parseResponse(body)
	if !errors.Is(err, ErrMalformedJSON) {
		t.Fatalf("err = %v, want ErrMalformedJSON", err)
	}
}

func TestHealth(t *testing.T) {
	const modelID = "mistralai/ministral-3-3b"

	tests := []struct {
		name       string
		statusCode int
		body       string
		want       string
	}{
		{"loaded", 200, `{"id":"mistralai/ministral-3-3b","state":"loaded"}`, "loaded"},
		{"not_loaded", 200, `{"id":"mistralai/ministral-3-3b","state":"not-loaded"}`, "not_loaded"},
		{"not_installed_404", 404, `{"error":"not found"}`, "not_installed"},
		{"server_error_500", 500, `internal error`, "server_down"},
		{"malformed_body", 200, `not json`, "server_down"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// r.URL.Path is decoded; the actual request line carries %2F.
				// Assert the escaped form to confirm we encoded the slash.
				if r.URL.EscapedPath() != "/api/v0/models/mistralai%2Fministral-3-3b" {
					t.Errorf("escaped path = %q, want /api/v0/models/mistralai%%2Fministral-3-3b", r.URL.EscapedPath())
				}
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			c := NewClient(srv.URL+"/v1", modelID)
			if got := c.Health(); got != tt.want {
				t.Errorf("Health() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHealth_ServerDown(t *testing.T) {
	// Use a port that's almost certainly closed to simulate unreachable server.
	c := NewClient("http://127.0.0.1:1/v1", "any-model")
	if got := c.Health(); got != "server_down" {
		t.Errorf("Health() = %q, want server_down", got)
	}
}

func TestHealth_BaseURLWithoutV1Suffix(t *testing.T) {
	// Even if baseURL doesn't end in /v1, Health should still produce a sensible path.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v0/models/") {
			t.Errorf("path = %q, want prefix /api/v0/models/", r.URL.Path)
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"state":"loaded"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "any-model")
	if got := c.Health(); got != "loaded" {
		t.Errorf("Health() = %q, want loaded", got)
	}
}

// fakeLMStudio builds an httptest server that simulates LM Studio's
// /api/v0/models/{model}, /api/v1/models/load, and /v1/chat/completions
// endpoints. Tests control model state via the returned *state pointer.
type fakeLMStudio struct {
	server       *httptest.Server
	model       string
	state       atomic.Value // string: "not-loaded" or "loaded"
	instanceID  string
	loadCalls   atomic.Int32
	unloadCalls atomic.Int32
}

func newFakeLMStudio(t *testing.T, initial string) *fakeLMStudio {
	t.Helper()
	f := &fakeLMStudio{model: "test/model-1b", instanceID: "test/model-1b"}
	f.state.Store(initial)
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v0/models/"):
			if r.URL.EscapedPath() == "/api/v0/models/test%2Fmodel-1b" {
				w.Write([]byte(`{"id":"test/model-1b","state":"` + f.state.Load().(string) + `"}`))
				return
			}
			w.WriteHeader(404)
		case r.URL.Path == "/api/v1/models/load":
			f.loadCalls.Add(1)
			f.state.Store("loaded")
			w.Write([]byte(`{"instance_id":"test/model-1b","status":"loaded","load_time_seconds":0.1}`))
		case r.URL.Path == "/api/v1/models/unload":
			f.unloadCalls.Add(1)
			f.state.Store("not-loaded")
			w.Write([]byte(`{"instance_id":"test/model-1b"}`))
		case r.URL.Path == "/v1/chat/completions":
			w.Write([]byte(`{"choices":[{"message":{"content":"{\"company\":\"X\",\"pay_period\":\"June 2026\",\"earnings\":[],\"deductions\":[],\"totals\":{},\"other\":{}}"}}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeLMStudio) baseURL() string { return f.server.URL + "/v1" }

// noopStarter satisfies the serverStarter seam without spawning anything.
func noopStarter(_ context.Context) error { return nil }

func TestExtract_AutoLoadsOnFirstCall(t *testing.T) {
	f := newFakeLMStudio(t, "not-loaded")
	var starterCalled atomic.Bool
	c := NewClient(f.baseURL(), f.model,
		WithServerStarter(func(ctx context.Context) error { starterCalled.Store(true); return nil }),
		WithIdleTimeout(time.Hour),
	)
	if _, err := c.Extract([]byte("img")); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if starterCalled.Load() {
		t.Error("server starter should not be called when server is already up")
	}
	if f.loadCalls.Load() != 1 {
		t.Errorf("load calls = %d, want 1", f.loadCalls.Load())
	}
	if f.unloadCalls.Load() != 0 {
		t.Errorf("unload calls = %d, want 0", f.unloadCalls.Load())
	}
	if got := c.Health(); got != "loaded" {
		t.Errorf("Health after extract = %q, want loaded", got)
	}
}

func TestExtract_AutoStartsServerWhenDown(t *testing.T) {
	f := newFakeLMStudio(t, "not-loaded")
	var starterCalled atomic.Bool
	// Point at a closed port initially; the starter re-points at the real server.
	var c *Client
	c = NewClient("http://127.0.0.1:1/v1", f.model,
		WithServerStarter(func(ctx context.Context) error {
			starterCalled.Store(true)
			c.baseURL = f.baseURL()
			return nil
		}),
		WithIdleTimeout(time.Hour),
	)
	if _, err := c.Extract([]byte("img")); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !starterCalled.Load() {
		t.Error("server starter was not called when server was down")
	}
	if f.loadCalls.Load() != 1 {
		t.Errorf("load calls = %d, want 1", f.loadCalls.Load())
	}
}

func TestExtract_ModelNotInstalled(t *testing.T) {
	// Server that 404s the model lookup.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()
	c := NewClient(srv.URL+"/v1", "nope/model",
		WithServerStarter(noopStarter),
	)
	_, err := c.Extract([]byte("img"))
	if !errors.Is(err, ErrNoModel) {
		t.Errorf("err = %v, want ErrNoModel", err)
	}
}

func TestExtract_ServerStartFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404) // pretend model not found (server is up but we shouldn't get this far)
	}))
	defer srv.Close()
	c := NewClient("http://127.0.0.1:1/v1", "any/model",
		WithServerStarter(func(ctx context.Context) error { return errors.New("lms: not found on PATH") }),
	)
	_, err := c.Extract([]byte("img"))
	if !errors.Is(err, ErrServerStartFailed) {
		t.Errorf("err = %v, want ErrServerStartFailed", err)
	}
}

func TestExtract_ServerStartTimeout(t *testing.T) {
	// Starter returns nil but the server stays unreachable (port 1).
	c := NewClient("http://127.0.0.1:1/v1", "any/model",
		WithServerStarter(noopStarter),
	)
	// Shorten the timeout so we don't wait 60s.
	// We can't easily inject serverStartTimeout without another option,
	// so this test is a smoke check: starter ran, Extract eventually errored.
	// Skip in short mode since it polls for ~60s.
	if testing.Short() {
		t.Skip("skipping 60s timeout test in short mode")
	}
	_, err := c.Extract([]byte("img"))
	if !errors.Is(err, ErrServerStartTimeout) {
		t.Errorf("err = %v, want ErrServerStartTimeout", err)
	}
}

func TestIdleUnload_AfterTimeout(t *testing.T) {
	f := newFakeLMStudio(t, "not-loaded")
	c := NewClient(f.baseURL(), f.model,
		WithServerStarter(noopStarter),
		WithIdleTimeout(100*time.Millisecond),
	)
	c.Start()
	defer c.Stop()

	if _, err := c.Extract([]byte("img")); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if f.unloadCalls.Load() != 0 {
		t.Fatalf("unload calls before idle = %d, want 0", f.unloadCalls.Load())
	}

	// Wait for idle timer to fire. Interval is idleTimeout/2 = 50ms.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f.unloadCalls.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if f.unloadCalls.Load() < 1 {
		t.Fatalf("unload calls after idle = %d, want ≥1", f.unloadCalls.Load())
	}
	if got := c.Health(); got != "not_loaded" {
		t.Errorf("Health after idle unload = %q, want not_loaded", got)
	}
}

func TestIdleUnload_DoesNotFireWhileBusy(t *testing.T) {
	f := newFakeLMStudio(t, "not-loaded")
	c := NewClient(f.baseURL(), f.model,
		WithServerStarter(noopStarter),
		WithIdleTimeout(100*time.Millisecond),
	)
	c.Start()
	defer c.Stop()

	if _, err := c.Extract([]byte("img")); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	// Immediately run another extract — should not have been unloaded.
	if f.unloadCalls.Load() != 0 {
		t.Fatalf("unload calls before second extract = %d, want 0", f.unloadCalls.Load())
	}
	if _, err := c.Extract([]byte("img")); err != nil {
		t.Fatalf("second Extract: %v", err)
	}
}

func TestHealth_ReportsTransitionalStates(t *testing.T) {
	// During a long-running load, Health should report "model_loading".
	// We simulate by making the load endpoint block until a signal is sent.
	loadStarted := make(chan struct{})
	loadProceed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v0/models/"):
			w.Write([]byte(`{"id":"m","state":"not-loaded"}`))
		case r.URL.Path == "/api/v1/models/load":
			close(loadStarted)
			<-loadProceed
			w.Write([]byte(`{"instance_id":"m","status":"loaded","load_time_seconds":0.1}`))
		case r.URL.Path == "/v1/chat/completions":
			w.Write([]byte(`{"choices":[{"message":{"content":"{\"company\":\"X\"}"}}]}`))
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL+"/v1", "m",
		WithServerStarter(noopStarter),
		WithIdleTimeout(time.Hour),
	)
	extractDone := make(chan error, 1)
	go func() {
		_, err := c.Extract([]byte("img"))
		extractDone <- err
	}()

	<-loadStarted
	// While load is mid-flight, Health should report model_loading.
	if got := c.Health(); got != "model_loading" {
		t.Errorf("Health during load = %q, want model_loading", got)
	}
	close(loadProceed)
	if err := <-extractDone; err != nil {
		t.Fatalf("Extract: %v", err)
	}
}

func TestParseClassifyResponse_Valid(t *testing.T) {
	body := wrapContent(t, Classification{
		Earnings:   []string{"basic", "basic", "hra"},
		Deductions: []string{"term_insurance_deduction", "epf"},
	})
	got, err := parseClassifyResponse(body)
	if err != nil {
		t.Fatalf("parseClassifyResponse: %v", err)
	}
	if len(got.Earnings) != 3 || got.Earnings[0] != "basic" || got.Earnings[2] != "hra" {
		t.Errorf("Earnings = %+v", got.Earnings)
	}
	if len(got.Deductions) != 2 || got.Deductions[0] != "term_insurance_deduction" {
		t.Errorf("Deductions = %+v", got.Deductions)
	}
}

func TestParseClassifyResponse_MarkdownFences(t *testing.T) {
	innerJSON := `{"earnings":["basic"],"deductions":["epf"]}`
	content := "```json\n" + innerJSON + "\n```"
	outer := map[string]interface{}{
		"choices": []map[string]interface{}{
			{"message": map[string]interface{}{"content": content}},
		},
	}
	body, _ := json.Marshal(outer)

	got, err := parseClassifyResponse(body)
	if err != nil {
		t.Fatalf("parseClassifyResponse: %v", err)
	}
	if len(got.Earnings) != 1 || got.Earnings[0] != "basic" {
		t.Errorf("Earnings = %+v", got.Earnings)
	}
}

func TestParseClassifyResponse_Malformed(t *testing.T) {
	outer := map[string]interface{}{
		"choices": []map[string]interface{}{
			{"message": map[string]interface{}{"content": "not json"}},
		},
	}
	body, _ := json.Marshal(outer)
	_, err := parseClassifyResponse(body)
	if !errors.Is(err, ErrMalformedJSON) {
		t.Fatalf("err = %v, want ErrMalformedJSON", err)
	}
}

func TestBuildClassifyInput_GroupsByCategory(t *testing.T) {
	ext := &Extraction{
		Earnings:   []Component{{Label: "Basic", Amount: 5000}},
		Deductions: []Component{{Label: "EPF", Amount: 600}},
	}
	canonicals := []CanonicalRef{
		{Slug: "basic", Name: "Basic", Category: "earning"},
		{Slug: "hra", Name: "HRA", Category: "earning"},
		{Slug: "epf", Name: "EPF", Category: "deduction"},
		{Slug: "tds", Name: "TDS", Category: "deduction"},
	}
	input := buildClassifyInput(ext, canonicals)
	if len(input.Earnings) != 1 || input.Earnings[0].Label != "Basic" {
		t.Errorf("Earnings = %+v", input.Earnings)
	}
	if len(input.Deductions) != 1 || input.Deductions[0].Label != "EPF" {
		t.Errorf("Deductions = %+v", input.Deductions)
	}
	if len(input.Canonicals.Earnings) != 2 {
		t.Errorf("canonical earnings = %d, want 2", len(input.Canonicals.Earnings))
	}
	if len(input.Canonicals.Deductions) != 2 {
		t.Errorf("canonical deductions = %d, want 2", len(input.Canonicals.Deductions))
	}
	if input.Canonicals.Earnings[0].Slug != "basic" {
		t.Errorf("first earning canonical slug = %q, want basic", input.Canonicals.Earnings[0].Slug)
	}
	if input.Canonicals.Deductions[0].Slug != "epf" {
		t.Errorf("first deduction canonical slug = %q, want epf", input.Canonicals.Deductions[0].Slug)
	}
}
