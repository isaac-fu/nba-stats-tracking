package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ballDontLieAPIBaseURL = "https://api.balldontlie.io/v1"

	// The free/trial BALLDONTLIE tier allows five requests per minute. Four
	// requests per minute leaves a little headroom for clock and network
	// differences, while paid tiers can override this with an environment
	// variable.
	defaultBALLDontLieRequestsPerMinute = 4
	defaultBALLDontLieMaxRetries        = 4
	defaultBALLDontLieHTTPTimeout       = 30 * time.Second
	defaultTooManyRequestsWait          = time.Minute
	defaultAPIPageSize                  = 100
)

// PGCooler serializes outbound API requests and spaces them out so that a
// paginated import does not burst through the BALLDONTLIE request quota. It
// also retries rate-limit and temporary server responses.
//
// The name is kept local to this application: PostgreSQL connection pooling
// cannot prevent an HTTP API from returning 429.
type PGCooler struct {
	client     *http.Client
	interval   time.Duration
	maxRetries int

	mu          sync.Mutex
	nextRequest time.Time
}

func NewPGCooler(
	client *http.Client,
	requestsPerMinute int,
	maxRetries int,
) *PGCooler {
	if client == nil {
		client = &http.Client{Timeout: defaultBALLDontLieHTTPTimeout}
	}

	if requestsPerMinute <= 0 {
		requestsPerMinute = defaultBALLDontLieRequestsPerMinute
	}

	if maxRetries < 0 {
		maxRetries = 0
	}

	return &PGCooler{
		client:     client,
		interval:   time.Minute / time.Duration(requestsPerMinute),
		maxRetries: maxRetries,
	}
}

func newPGCoolerFromEnv() *PGCooler {
	requestsPerMinute := envInt(
		"BALLDONTLIE_REQUESTS_PER_MINUTE",
		defaultBALLDontLieRequestsPerMinute,
	)
	maxRetries := envInt(
		"BALLDONTLIE_MAX_RETRIES",
		defaultBALLDontLieMaxRetries,
	)
	timeoutSeconds := envInt("BALLDONTLIE_HTTP_TIMEOUT_SECONDS", 30)

	return NewPGCooler(
		&http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second},
		requestsPerMinute,
		maxRetries,
	)
}

// Do waits for a globally shared request slot before every attempt. A 429 is
// retried after Retry-After when supplied; otherwise it waits one minute.
// 408, 425, and 5xx responses use a short exponential backoff. The same
// cooler is shared by every importer, so the quota is global to the process.
func (c *PGCooler) Do(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("cannot execute a nil HTTP request")
	}

	for attempt := 0; ; attempt++ {
		if err := c.waitForRequestSlot(req.Context()); err != nil {
			return nil, err
		}

		resp, err := c.client.Do(req)
		if err == nil && !shouldRetryResponse(resp) {
			return resp, nil
		}

		if req.Context().Err() != nil {
			if resp != nil && resp.Body != nil {
				resp.Body.Close()
			}
			return nil, req.Context().Err()
		}

		if attempt >= c.maxRetries {
			// Leave a retryable response open when returning it to the caller so
			// the caller can read an API error body. Network-error responses are
			// closed here when net/http returned both a response and an error.
			if resp != nil && err != nil && resp.Body != nil {
				resp.Body.Close()
			}
			return resp, err
		}

		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}

		delay := c.retryDelay(resp, attempt)
		if err := sleepWithContext(req.Context(), delay); err != nil {
			return nil, err
		}
	}
}

func (c *PGCooler) waitForRequestSlot(ctx context.Context) error {
	c.mu.Lock()

	now := time.Now()
	nextRequest := now
	if c.nextRequest.After(nextRequest) {
		nextRequest = c.nextRequest
	}
	c.nextRequest = nextRequest.Add(c.interval)

	c.mu.Unlock()

	return sleepUntil(ctx, nextRequest)
}

func (c *PGCooler) retryDelay(resp *http.Response, attempt int) time.Duration {
	if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
		if delay, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok {
			if delay < c.interval {
				return c.interval
			}
			return delay
		}

		if defaultTooManyRequestsWait < c.interval {
			return c.interval
		}
		return defaultTooManyRequestsWait
	}

	backoff := time.Second
	for i := 0; i < attempt && backoff < 30*time.Second; i++ {
		backoff *= 2
	}
	if backoff > 30*time.Second {
		return 30 * time.Second
	}
	return backoff
}

func shouldRetryResponse(resp *http.Response) bool {
	if resp == nil {
		return false
	}

	return resp.StatusCode == http.StatusRequestTimeout ||
		resp.StatusCode == http.StatusTooEarly ||
		resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode >= 500
}

func parseRetryAfter(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}

	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, true
	}

	retryAt, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}

	delay := time.Until(retryAt)
	if delay < 0 {
		delay = 0
	}
	return delay, true
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func sleepUntil(ctx context.Context, deadline time.Time) error {
	return sleepWithContext(ctx, time.Until(deadline))
}

// getAPIJSON centralizes request construction, authentication, response
// closing, and useful error bodies. Keeping this in one place prevents one
// importer from accidentally bypassing the retry/rate-limit policy.
func getAPIJSON(
	ctx context.Context,
	cooler *PGCooler,
	apiKey string,
	endpoint string,
	params url.Values,
	destination any,
) error {
	apiURL := ballDontLieAPIBaseURL + endpoint
	if encoded := params.Encode(); encoded != "" {
		apiURL += "?" + encoded
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := cooler.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		if readErr != nil {
			return fmt.Errorf("%s %s (reading error body: %w)", endpoint, resp.Status, readErr)
		}
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = "no response body"
		}
		return fmt.Errorf("%s request failed: %s: %s", endpoint, resp.Status, message)
	}

	if err := json.NewDecoder(resp.Body).Decode(destination); err != nil {
		return fmt.Errorf("decoding %s response: %w", endpoint, err)
	}
	return nil
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func envBool(name string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

// The API's current NBA IDs are intentionally explicit. A numeric range is
// easy to misread and would silently accept an unrelated team if the provider
// ever inserted a team into that range.
var currentNBAAbbreviations = map[int]string{
	1:  "ATL",
	2:  "BOS",
	3:  "BKN",
	4:  "CHA",
	5:  "CHI",
	6:  "CLE",
	7:  "DAL",
	8:  "DEN",
	9:  "DET",
	10: "GSW",
	11: "HOU",
	12: "IND",
	13: "LAC",
	14: "LAL",
	15: "MEM",
	16: "MIA",
	17: "MIL",
	18: "MIN",
	19: "NOP",
	20: "NYK",
	21: "OKC",
	22: "ORL",
	23: "PHI",
	24: "PHX",
	25: "POR",
	26: "SAC",
	27: "SAS",
	28: "TOR",
	29: "UTA",
	30: "WAS",
}

func isCurrentNBATeamID(id int) bool {
	_, ok := currentNBAAbbreviations[id]
	return ok
}

func isCurrentNBATeam(id int, abbreviation string) bool {
	expected, ok := currentNBAAbbreviations[id]
	if !ok {
		return false
	}

	abbreviation = strings.ToUpper(strings.TrimSpace(abbreviation))
	return abbreviation == "" || abbreviation == expected
}

// These are historical names for franchises that still exist today.
// Relocated franchises remain valid for a 1980-present dataset; permanently
// defunct franchises are excluded separately below.
var relocatedNBAFranchiseTeamNames = map[string]struct{}{
	"baltimore bullets":                 {},
	"buffalo braves":                    {},
	"capital bullets":                   {},
	"charlotte bobcats":                 {},
	"chicago packers":                   {},
	"chicago zephyrs":                   {},
	"cincinnati royals":                 {},
	"fort wayne pistons":                {},
	"kansas city kings":                 {},
	"kansas city omaha kings":           {},
	"milwaukee hawks":                   {},
	"minneapolis lakers":                {},
	"new jersey nets":                   {},
	"new orleans hornets":               {},
	"new orleans jazz":                  {},
	"new orleans oklahoma city hornets": {},
	"new york nets":                     {},
	"philadelphia warriors":             {},
	"rochester royals":                  {},
	"san diego clippers":                {},
	"san diego rockets":                 {},
	"san francisco warriors":             {},
	"seattle supersonics":               {},
	"st louis hawks":                    {},
	"syracuse nationals":                {},
	"tri cities blackhawks":             {},
	"vancouver grizzlies":               {},
	"washington bullets":                {},
}

// These IDs are the permanently defunct teams shown by the API in the
// historical NBA/BAA block. Baltimore Bullets is a name shared by a defunct
// team and the later relocated Washington franchise, so the known defunct ID
// must remain excluded even though the name is otherwise an allowed alias.
var permanentlyDefunctNBATeamIDs = map[int]struct{}{
	37: {}, // Chicago Stags
	38: {}, // St. Louis Bombers
	39: {}, // Cleveland Rebels
	40: {}, // Detroit Falcons
	41: {}, // Toronto Huskies
	42: {}, // Washington Capitols
	43: {}, // Providence Steamrollers
	44: {}, // Pittsburgh Ironmen
	45: {}, // Baltimore Bullets (1947-1950)
	46: {}, // Indianapolis Jets
	47: {}, // Anderson Packers
	48: {}, // Waterloo Hawks
	49: {}, // Indianapolis Olympians
	50: {}, // Denver Nuggets (1949-1950)
	51: {}, // Sheboygan Redskins
}

func isEligibleNBATeam(id int, fullName string, abbreviation string) bool {
	if _, permanentlyDefunct := permanentlyDefunctNBATeamIDs[id]; permanentlyDefunct {
		return false
	}

	if isCurrentNBATeam(id, abbreviation) {
		return true
	}

	_, relocated := relocatedNBAFranchiseTeamNames[normalizeTeamName(fullName)]
	return relocated
}

func normalizeTeamName(value string) string {
	replacer := strings.NewReplacer(
		"-", " ",
		"–", " ",
		"/", " ",
		".", "",
	)
	return strings.Join(
		strings.Fields(strings.ToLower(replacer.Replace(value))),
		" ",
	)
}

func defaultNBASeason(now time.Time) int {
	// NBA season labels refer to the year in which the season starts. Before
	// October, the currently active season still began in the prior year.
	if now.Month() >= time.October {
		return now.Year()
	}
	return now.Year() - 1
}
