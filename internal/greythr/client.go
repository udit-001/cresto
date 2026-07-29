// Package greythr is the greytHR ESS portal adapter. It uses cookie-based
// session auth (the user logs into the ESS portal in their browser, then
// pastes the access_token cookie into Cresto) to call greytHR's internal
// JSON APIs for listing and downloading payslips.
//
// The key insight is that greytHR's /v3/api/payroll/payslip/{profile_id}/
// {payslip_id}/published endpoint returns the full payslip breakdown as
// structured JSON — no PDF download or LLM vision extraction needed. The
// PDF is still downloaded for archival so the review UI can display it.
package greythr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var ErrNotConnected = errors.New("greythr: not connected (session missing or cookie expired)")

// Client talks to greytHR's ESS portal APIs. Construct one per app lifetime
// and share across handlers; all methods are safe for concurrent use.
type Client struct {
	sessionPath string
	httpClient   *http.Client
}

func New(sessionPath string) *Client {
	return &Client{
		sessionPath: sessionPath,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Session is the on-disk JSON shape for the persisted greytHR session.
type Session struct {
	Host       string    `json:"host"`
	AccessToken string   `json:"access_token"`
	ProfileID  int       `json:"profile_id"`
	Email      string    `json:"email,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func (c *Client) Connected() bool {
	_, err := c.LoadSession()
	return err == nil
}

// LoadSession reads and returns the persisted session. Returns an error if
// the session file is missing or incomplete.
func (c *Client) LoadSession() (*Session, error) {
	return c.loadSession()
}

// SaveSession normalises the cookie value (stripping the "access_token="
// prefix if the user pasted the full cookie string) and persists the session.
func (c *Client) SaveSession(host, accessToken string, profileID int) error {
	host = normaliseHost(host)
	accessToken = extractToken(accessToken)
	sess := &Session{
		Host:       host,
		AccessToken: accessToken,
		ProfileID:  profileID,
		CreatedAt:  time.Now(),
	}
	return c.writeSession(sess)
}

func (c *Client) Disconnect() error {
	err := os.Remove(c.sessionPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove session: %w", err)
	}
	return nil
}

// PayslipMonth is one published payslip period from the months endpoint.
type PayslipMonth struct {
	ID       int    `json:"id"`
	Month    string `json:"month"`     // "Jun 2026"
	FromDate string `json:"fromDate"`  // "2026-06-01"
	ToDate   string `json:"toDate"`    // "2026-06-30"
	Released bool   `json:"released"`
}

// MonthsResponse is the response from GET /v3/api/payroll/months/{id}/published.
type MonthsResponse struct {
	Months       []PayslipMonth `json:"months"`
	EmployeeInfo struct {
		EmployeeID int    `json:"employeeId"`
		Email      string `json:"email"`
	} `json:"employeeInfo"`
}

// ListPayslipMonths calls the months endpoint and returns all published
// payslip periods. On success, it also enriches the session with the
// employee's email if not already set.
func (c *Client) ListPayslipMonths(ctx context.Context) (*MonthsResponse, error) {
	sess, err := c.loadSession()
	if err != nil {
		return nil, ErrNotConnected
	}

	url := fmt.Sprintf("https://%s/v3/api/payroll/months/%d/published?type=payslip", sess.Host, sess.ProfileID)
	resp, err := c.doGet(ctx, sess, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, ErrNotConnected
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("list months: HTTP %d", resp.StatusCode)
	}

	var result MonthsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode months response: %w", err)
	}

	if sess.Email == "" && result.EmployeeInfo.Email != "" {
		sess.Email = result.EmployeeInfo.Email
		_ = c.writeSession(sess)
	}

	return &result, nil
}

// PayslipData is the response from GET /v3/api/payroll/payslip/{id}/{pid}/published.
// It contains the full payslip breakdown as structured JSON.
type PayslipData struct {
	Content       []PayslipItem `json:"content"`
	NetPayInWords string        `json:"netPayInWords"`
	CTCTypeText   string        `json:"ctcTypeText"`
}

type PayslipItem struct {
	Item  PayslipItemDef `json:"item"`
	Value float64        `json:"value"`
}

type PayslipItemDef struct {
	ID          int64  `json:"id"`
	Parent      string `json:"parent"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SalaryType  int    `json:"salaryType"`
	Show        bool   `json:"show"`
}

// EmployeeInfo holds employee metadata fetched from greytHR's Employment &
// Job and personal-data endpoints. Used to prefill payslips during the
// greythr fetch flow.
type EmployeeInfo struct {
	Designation string
	EmployeeNo  string
}

// FetchEmployeeInfo fetches the employee's current designation (from the
// Employment & Job endpoint) and employee number (from the personal-data
// endpoint). Called once per fetch run to prefill payslip data.
//
// Any transport, HTTP, or decode failure from either endpoint is returned as
// an error; callers treat the whole call as best-effort and continue with
// empty values. A nil error with empty fields means the field is genuinely
// absent from the profile — not that a call silently failed.
func (c *Client) FetchEmployeeInfo(ctx context.Context) (EmployeeInfo, error) {
	sess, err := c.loadSession()
	if err != nil {
		return EmployeeInfo{}, ErrNotConnected
	}

	var info EmployeeInfo

	// Designation: GET /core-hr/v1/empandjob/data/{id}
	empURL := fmt.Sprintf("https://%s/core-hr/v1/empandjob/data/%d", sess.Host, sess.ProfileID)
	empBody, err := c.getRaw(ctx, sess, empURL)
	if err != nil {
		return EmployeeInfo{}, fmt.Errorf("fetch designation: %w", err)
	}
	designation, err := parseDesignation(empBody)
	if err != nil {
		return EmployeeInfo{}, fmt.Errorf("parse designation: %w", err)
	}
	info.Designation = designation

	// Employee number: GET /v3/api/empinfo/personal/data/{id}
	personalURL := fmt.Sprintf("https://%s/v3/api/empinfo/personal/data/%d", sess.Host, sess.ProfileID)
	personalBody, err := c.getRaw(ctx, sess, personalURL)
	if err != nil {
		return EmployeeInfo{}, fmt.Errorf("fetch employee number: %w", err)
	}
	employeeNo, err := parseEmployeeNo(personalBody)
	if err != nil {
		return EmployeeInfo{}, fmt.Errorf("parse employee number: %w", err)
	}
	info.EmployeeNo = employeeNo

	return info, nil
}

// parseDesignation extracts the current designation from the
// /core-hr/v1/empandjob/data/{id} response body. Returns "" (no error) if
// the designation field is absent from the employee's profile.
func parseDesignation(body []byte) (string, error) {
	var result struct {
		PanelData struct {
			CurrentPosition []struct {
				Sections struct {
					CurrentProfile struct {
						Data []map[string]any `json:"data"`
					} `json:"current-profile"`
				} `json:"sections"`
			} `json:"currentposition"`
		} `json:"panelData"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode designation response: %w", err)
	}
	for _, cp := range result.PanelData.CurrentPosition {
		for _, row := range cp.Sections.CurrentProfile.Data {
			if d, ok := row["currentTransitions$extEmpInfo$c_designation"].(string); ok && d != "" {
				return d, nil
			}
		}
	}
	return "", nil
}

// parseEmployeeNo extracts the employee number from the
// /v3/api/empinfo/personal/data/{id} response body. The response is a
// top-level object keyed by panel name (address, profile, …); we search all
// panels and sections for the employeeno field. Returns "" (no error) if
// absent.
func parseEmployeeNo(body []byte) (string, error) {
	var result map[string][]struct {
		Sections map[string]struct {
			Data []map[string]any `json:"data"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode employee number response: %w", err)
	}
	for _, panels := range result {
		for _, panel := range panels {
			for _, section := range panel.Sections {
				for _, row := range section.Data {
					if e, ok := row["basicInformation$emp1$employeeno"].(string); ok && e != "" {
						return e, nil
					}
				}
			}
		}
	}
	return "", nil
}

// FetchPayslipData calls the published endpoint and returns the full payslip
// breakdown as structured JSON.
func (c *Client) FetchPayslipData(ctx context.Context, payslipID int) (*PayslipData, error) {
	sess, err := c.loadSession()
	if err != nil {
		return nil, ErrNotConnected
	}

	url := fmt.Sprintf("https://%s/v3/api/payroll/payslip/%d/%d/published", sess.Host, sess.ProfileID, payslipID)
	resp, err := c.doGet(ctx, sess, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, ErrNotConnected
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fetch payslip data: HTTP %d", resp.StatusCode)
	}

	var result PayslipData
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode payslip data: %w", err)
	}
	return &result, nil
}

// DownloadPayslipPDF downloads the PDF for archival. Returns the body
// (caller must close) and the filename from Content-Disposition.
func (c *Client) DownloadPayslipPDF(ctx context.Context, payslipID int) (io.ReadCloser, string, error) {
	sess, err := c.loadSession()
	if err != nil {
		return nil, "", ErrNotConnected
	}

	url := fmt.Sprintf("https://%s/v3/api/payroll/payslip/%d/%d/download", sess.Host, sess.ProfileID, payslipID)
	resp, err := c.doGet(ctx, sess, url)
	if err != nil {
		return nil, "", err
	}

	if resp.StatusCode == 401 {
		resp.Body.Close()
		return nil, "", ErrNotConnected
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, "", fmt.Errorf("download payslip pdf: HTTP %d", resp.StatusCode)
	}

	filename := fmt.Sprintf("payslip_%d.pdf", payslipID)
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if idx := strings.Index(cd, "filename="); idx >= 0 {
			filename = strings.Trim(cd[idx+9:], `"`)
		}
	}
	// Prefix with subdomain for multi-employer disambiguation.
	if idx := strings.Index(sess.Host, "."); idx > 0 {
		filename = sess.Host[:idx] + "_" + filename
	}

	return resp.Body, filename, nil
}

// --- internal helpers ---

// getRaw performs an authenticated GET and returns the response body.
// Returns ErrNotConnected on 401. Internal seam used by FetchEmployeeInfo
// so transport and parsing can be tested independently.
func (c *Client) getRaw(ctx context.Context, sess *Session, url string) ([]byte, error) {
	resp, err := c.doGet(ctx, sess, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return nil, ErrNotConnected
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (c *Client) doGet(ctx context.Context, sess *Session, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Cookie", "access_token="+sess.AccessToken)
	req.Header.Set("accept", "application/json")
	req.Header.Set("x-requested-with", "XMLHttpRequest")
	req.Header.Set("referer", fmt.Sprintf("https://%s/v3/portal/ess/payroll/payslips/payslip", sess.Host))
	return c.httpClient.Do(req)
}

func (c *Client) loadSession() (*Session, error) {
	data, err := os.ReadFile(c.sessionPath)
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("parse session: %w", err)
	}
	if sess.AccessToken == "" || sess.Host == "" || sess.ProfileID == 0 {
		return nil, fmt.Errorf("incomplete session")
	}
	return &sess, nil
}

func (c *Client) writeSession(sess *Session) error {
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	if err := os.WriteFile(c.sessionPath, data, 0600); err != nil {
		return fmt.Errorf("write session: %w", err)
	}
	return nil
}

func normaliseHost(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimSuffix(host, "/")
	return host
}

// extractToken handles both "access_token=ory_at_..." and bare "ory_at_..."
// inputs — the user might paste the full cookie or just the token value.
func extractToken(raw string) string {
	raw = strings.TrimSpace(raw)
	if idx := strings.Index(raw, "access_token="); idx >= 0 {
		raw = raw[idx+len("access_token="):]
		if idx := strings.Index(raw, ";"); idx >= 0 {
			raw = raw[:idx]
		}
	}
	return strings.TrimSpace(raw)
}
