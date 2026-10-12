package main

import (
	"net/url"
	"sort"
	"strconv"
)

func addTeamFilters(params url.Values, teamIDs map[int]struct{}) {
	ids := make([]int, 0, len(teamIDs))
	for teamID := range teamIDs {
		ids = append(ids, teamID)
	}
	sort.Ints(ids)

	for _, teamID := range ids {
		params.Add("team_ids[]", strconv.Itoa(teamID))
	}
}

func addGameIDFilters(params url.Values, gameIDs []int) {
	ids := append([]int(nil), gameIDs...)
	sort.Ints(ids)

	seen := make(map[int]struct{}, len(ids))
	for _, gameID := range ids {
		if _, ok := seen[gameID]; ok {
			continue
		}
		seen[gameID] = struct{}{}
		params.Add("game_ids[]", strconv.Itoa(gameID))
	}
}
