package handlers

import (
	"context"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

type RoboSession struct {
	CurrentContext       context.Context
	CancelCurrentContext context.CancelFunc
	Connection           *websocket.Conn
}

func HandleStartSession(
	w http.ResponseWriter,
	r *http.Request,
	redisClient *redis.Client,
	conn *websocket.Conn,
) {

}
