package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

type PlayerTeam struct {
	ID int `json:"id"`
}

type Player struct {
	ID        int        `json:"id"`
	FirstName string     `json:"first_name"`
	LastName  string     `json:"last_name"`
	Team      PlayerTeam `json:"team"`
}

type PlayersResponse struct {
	Data []Player `json:"data"`
	Meta struct {
		NextCursor *int `json:"next_cursor"`
	} `json:"meta"`
}