package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type problem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	if status == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "30")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{"about:blank", http.StatusText(status), status, detail, r.URL.Path})
}

func dataError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNoRelease):
		writeProblem(w, r, http.StatusServiceUnavailable, "No statistics have been published yet. Run the data pipeline first.")
	case errors.Is(err, ErrMissingEntity):
		writeProblem(w, r, http.StatusNotFound, "One or more requested entities have no statistics for this season and phase.")
	default:
		// SQL details, passwords, hostnames, and connection strings stay private.
		writeProblem(w, r, http.StatusServiceUnavailable, "Statistics are temporarily unavailable. Try again shortly.")
	}
}

func getOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeProblem(w, r, http.StatusMethodNotAllowed, "This endpoint accepts GET requests.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		next(w, r.WithContext(ctx))
	}
}

func NewHandler(store Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", getOnly(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	mux.HandleFunc("/health/ready", getOnly(func(w http.ResponseWriter, r *http.Request) {
		_, err := store.Status(r.Context())
		if err != nil {
			dataError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}))
	mux.HandleFunc("/api/v1/status", getOnly(func(w http.ResponseWriter, r *http.Request) {
		if err := validateNoQuery(r); err != nil {
			writeProblem(w, r, 400, err.Error())
			return
		}
		status, err := store.Status(r.Context())
		if err != nil {
			dataError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, status)
	}))
	mux.HandleFunc("/api/v1/seasons", getOnly(func(w http.ResponseWriter, r *http.Request) {
		if err := validateNoQuery(r); err != nil {
			writeProblem(w, r, 400, err.Error())
			return
		}
		seasons, err := store.Seasons(r.Context())
		if err != nil {
			dataError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, seasons)
	}))
	for _, comparison := range []bool{false, true} {
		prefix := "/api/v1/"
		if comparison {
			prefix += "comparisons/"
		}
		mux.HandleFunc(prefix+"players", getOnly(func(w http.ResponseWriter, r *http.Request) {
			filters, err := parseFilters(r, comparison, false)
			if err != nil {
				writeProblem(w, r, 400, err.Error())
				return
			}
			players, err := store.Players(r.Context(), filters)
			if err != nil {
				dataError(w, r, err)
				return
			}
			writeJSON(w, http.StatusOK, players)
		}))
		mux.HandleFunc(prefix+"teams", getOnly(func(w http.ResponseWriter, r *http.Request) {
			filters, err := parseFilters(r, comparison, true)
			if err != nil {
				writeProblem(w, r, 400, err.Error())
				return
			}
			teams, err := store.Teams(r.Context(), filters)
			if err != nil {
				dataError(w, r, err)
				return
			}
			writeJSON(w, http.StatusOK, teams)
		}))
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, http.StatusNotFound, "No endpoint exists at this path.")
	})
	return mux
}

func validateNoQuery(r *http.Request) error {
	if r.URL.RawQuery != "" {
		return errors.New("This endpoint does not accept query parameters.")
	}
	return nil
}

var entityID = regexp.MustCompile(`^[A-Za-z0-9_:\-]+$`)

func parseFilters(r *http.Request, comparison, teams bool) (Filters, error) {
	f := Filters{Phase: "regular", Sort: "pts_per_game", Limit: 100}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return f, errors.New("The query string is invalid.")
	}
	allowed := map[string]bool{"season": true, "phase": true, "q": !comparison, "sort": !comparison, "limit": !comparison, "ids": comparison}
	for key, values := range q {
		if !allowed[key] {
			return f, fmt.Errorf("Unsupported query parameter: %s.", key)
		}
		if len(values) != 1 {
			return f, fmt.Errorf("Supply %s exactly once.", key)
		}
	}
	f.Season, err = strconv.Atoi(q.Get("season"))
	if err != nil || f.Season < 1947 || f.Season > 2200 {
		return f, errors.New("season must be an ending year between 1947 and 2200, such as 2024.")
	}
	if phase, present := q["phase"]; present {
		f.Phase = phase[0]
		if f.Phase != "regular" && f.Phase != "playoffs" {
			return f, errors.New("phase must be regular or playoffs.")
		}
	}
	if comparison {
		f.IDs = strings.Split(q.Get("ids"), ",")
		if len(f.IDs) < 2 || len(f.IDs) > 5 {
			return f, errors.New("Supply 2 to 5 unique comma-separated ids.")
		}
		seen := map[string]bool{}
		for _, id := range f.IDs {
			if len(id) > 100 || !entityID.MatchString(id) || seen[id] {
				return f, errors.New("ids must be unique valid identifiers with no spaces.")
			}
			seen[id] = true
		}
		f.Limit = 5
		return f, nil
	}
	f.Query = strings.TrimSpace(q.Get("q"))
	if !utf8.ValidString(f.Query) || utf8.RuneCountInString(f.Query) > 100 || strings.ContainsRune(f.Query, '\x00') {
		return f, errors.New("q must contain at most 100 valid characters.")
	}
	if sorts, present := q["sort"]; present {
		f.Sort = sorts[0]
		sortMap := playerSorts
		if teams {
			sortMap = teamSorts
		}
		if _, ok := sortMap[f.Sort]; !ok {
			return f, errors.New("sort is not supported for this endpoint.")
		}
	}
	if limits, present := q["limit"]; present {
		f.Limit, err = strconv.Atoi(limits[0])
		if err != nil || f.Limit < 1 || f.Limit > 1000 {
			return f, errors.New("limit must be between 1 and 1000.")
		}
	}
	return f, nil
}
