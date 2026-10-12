package main

type errorResponse struct {
	Error string `json:"error"`
}

type pageMeta struct {
	Limit      int  `json:"limit"`
	Offset     int  `json:"offset"`
	Count      int  `json:"count"`
	HasMore    bool `json:"has_more"`
}

type paginatedResponse[T any] struct {
	Data []T      `json:"data"`
	Meta pageMeta `json:"meta"`
}

type Team struct {
	ID           int    `json:"id"`
	ExternalID   int    `json:"external_id"`
	Name         string `json:"name"`
	Abbreviation string `json:"abbreviation"`
}

type Player struct {
	ID         int    `json:"id"`
	ExternalID int    `json:"external_id"`
	Name       string `json:"name"`
	Team       *Team  `json:"team,omitempty"`
}

type Game struct {
	ID         int       `json:"id"`
	ExternalID int       `json:"external_id"`
	GameDate   string    `json:"game_date"`
	HomeScore  int       `json:"home_score"`
	AwayScore  int       `json:"away_score"`
	HomeTeam   Team      `json:"home_team"`
	AwayTeam   Team      `json:"away_team"`
}

type PlayerGameStat struct {
	ID                            int    `json:"id"`
	GameID                        int    `json:"game_id"`
	GameDate                      string `json:"game_date"`
	PlayerID                      int    `json:"player_id"`
	PlayerName                    string `json:"player_name"`
	TeamID                        int    `json:"team_id"`
	TeamName                      string `json:"team_name"`
	TeamAbbreviation              string `json:"team_abbreviation"`
	Points                        int    `json:"points"`
	Rebounds                      int    `json:"rebounds"`
	Assists                       int    `json:"assists"`
	MinutesPlayedSeconds          int    `json:"minutes_played_seconds"`
	Steals                        int    `json:"steals"`
	Blocks                        int    `json:"blocks"`
	Turnovers                     int    `json:"turnovers"`
	FieldGoalsMade                int    `json:"field_goals_made"`
	FieldGoalsAttempted           int    `json:"field_goals_attempted"`
	ThreePointFieldGoalsMade      int    `json:"three_point_field_goals_made"`
	ThreePointFieldGoalsAttempted int    `json:"three_point_field_goals_attempted"`
	FreeThrowsMade                int    `json:"free_throws_made"`
	FreeThrowsAttempted            int    `json:"free_throws_attempted"`
}
