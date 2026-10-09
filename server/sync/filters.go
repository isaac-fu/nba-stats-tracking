package main

import (
	"net/url"
	"strconv"
)

func addTeamFilters(params url.Values, teamIDs map[int]struct{}) {
	for teamID := range teamIDs {
		params.Add("team_ids[]", strconv.Itoa(teamID))
	}
}