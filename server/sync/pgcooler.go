package main

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// The free/trial BALLDONTLIE tier allows five requests per minute. Four
	// requests per minute leaves a little headroom for clock and network
	// differences, while paid tiers can override this with an environment
	// variable.
	defaultBALLDontLieRequestsPerMinute = 4
	defaultBALLDontLieMaxRetries        = 4
	defaultBALLDontLieHTTPTimeout       = 30 * time.Second
	defaultTooManyRequestsWait          = time.Minute
)

// PGCooler serializes outbound API requests and spaces them out so that a
// paginated import does not burst through the BALLDONTLIE request quota.
// It also retries rate-limit and temporary server responses.
//
// The name is kept local to this application: there is no maintained Go
// dependency named pgcooler, and PostgreSQL connection pooling cannot prevent
// an HTTP API from returning 429.
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

	return NewPGCooler(
		&http.Client{Timeout: defaultBALLDontLieHTTPTimeout},
		requestsPerMinute,
		maxRetries,
	)
}

// Do waits for a globally shared request slot before every attempt. A 429 is
// retried after Retry-After when supplied; otherwise it waits one minute.
// 5xx responses use a short exponential backoff.
func (c *PGCooler) Do(req *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		if err := c.waitForRequestSlot(req.Context()); err != nil {
			return nil, err
		}

		resp, err := c.client.Do(req)
		if err == nil && !shouldRetryResponse(resp) {
			return resp, nil
		}

		if attempt >= c.maxRetries {
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
	return resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
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

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

// BALLDONTLIE uses IDs 1 through 30 for the current NBA franchises. Historical
// names for franchises that still exist are allowed separately below, so a
// relocation does not incorrectly cause that franchise's data to be dropped.
func isCurrentNBATeamID(id int) bool {
	return id >= 1 && id <= 30
}

// These are historical NBA/BAA names for franchises that still exist today.
// They are intentionally name-based because the API does not expose a stable
// franchise/relocation field on its team object.
var relocatedNBAFranchiseTeamNames = map[string]struct{}{
	"baltimore bullets":                {},
	"buffalo braves":                   {},
	"capital bullets":                  {},
	"chicago packers":                  {},
	"chicago zephyrs":                  {},
	"cincinnati royals":                {},
	"fort wayne pistons":               {},
	"kansas city kings":                {},
	"kansas city omaha kings":          {},
	"milwaukee hawks":                  {},
	"minneapolis lakers":                {},
	"new jersey nets":                  {},
	"new orleans hornets":              {},
	"new orleans jazz":                 {},
	"new orleans oklahoma city hornets": {},
	"new york nets":                    {},
	"philadelphia warriors":            {},
	"rochester royals":                 {},
	"san diego clippers":               {},
	"san diego rockets":                {},
	"san francisco warriors":           {},
	"seattle supersonics":              {},
	"st louis hawks":                   {},
	"syracuse nationals":               {},
	"tri cities blackhawks":            {},
	"vancouver grizzlies":              {},
	"washington bullets":               {},
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

func isEligibleNBATeam(id int, fullName string) bool {
	if _, permanentlyDefunct := permanentlyDefunctNBATeamIDs[id]; permanentlyDefunct {
		return false
	}

	if isCurrentNBATeamID(id) {
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
