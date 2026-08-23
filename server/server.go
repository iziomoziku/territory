package main

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
    pongWait   = 60 * time.Second
    pingPeriod = (pongWait * 9) / 10
    writeWait  = 10 * time.Second
)

type MessageType string
const (
	playerLeft    MessageType = "leave"
	playerState    MessageType = "state"
	playerJoined    MessageType = "join"
)

var upgrader = websocket.Upgrader{
    ReadBufferSize:  1024,
    WriteBufferSize: 1024,
    CheckOrigin: func(r *http.Request) bool { return true }, // dev only
}

type PlayerPosition struct {
    XPos int `json:"xPos"`
    YPos int `json:"yPos"`
}

type connectedPlayer struct {
    Position PlayerPosition
    Conn     *websocket.Conn
    WriteMu  sync.Mutex
}

type PlayerTracker struct {
	mu      sync.Mutex
	players map[string]*connectedPlayer
}


type Message struct {
    Type     MessageType                    `json:"type"`
    UserId   string                    `json:"userId,omitempty"`
    Position *PlayerPosition           `json:"position,omitempty"`
    Players  map[string]PlayerPosition `json:"players,omitempty"`
}


func broadcast(playerTracker *PlayerTracker, msg Message, excludeUserId string) {
    playerTracker.mu.Lock()
    recipients := make([]*connectedPlayer, 0, len(playerTracker.players))
    for userId, player := range playerTracker.players {
        if userId != excludeUserId {
            recipients = append(recipients, player)
        }
    }
    playerTracker.mu.Unlock()

    for _, player := range recipients {
        player.WriteMu.Lock()
        player.Conn.SetWriteDeadline(time.Now().Add(writeWait))
        if err := player.Conn.WriteJSON(msg); err != nil {
            log.Printf("broadcast error: %+v\n", err)
        }
        player.WriteMu.Unlock()
    }
}

func snapshot(playerTracker *PlayerTracker) map[string]PlayerPosition {
    playerTracker.mu.Lock()
    defer playerTracker.mu.Unlock()

    positions := make(map[string]PlayerPosition, len(playerTracker.players))
    for userId, player := range playerTracker.players {
        positions[userId] = player.Position
    }
    return positions
}

func disconnectPlayer(playerTracker *PlayerTracker, userId string) {
    playerTracker.mu.Lock()
    delete(playerTracker.players, userId)
    playerTracker.mu.Unlock()

    log.Printf("removed user from server: %s\n", userId)
    broadcast(playerTracker, Message{Type: playerLeft, UserId: userId}, userId)
}

func connectPlayer(playerTracker *PlayerTracker, userId string, playerPos PlayerPosition, conn *websocket.Conn) *connectedPlayer {
    player := &connectedPlayer{Position: playerPos, Conn: conn}

    playerTracker.mu.Lock()
    playerTracker.players[userId] = player
    playerTracker.mu.Unlock()

    log.Printf("player joined: %s %+v\n", userId, playerPos)
    broadcast(playerTracker, Message{Type: playerJoined, UserId: userId, Position: &playerPos}, userId)

    return player
}

func validate(playerTracker *PlayerTracker, userId string, xPos string, yPos string) (string, int, int, error) {

    playerTracker.mu.Lock()
    _, exists := playerTracker.players[userId]
    playerTracker.mu.Unlock()
    if exists {
        log.Printf("userId already exist, disconnecting previous userId\n",)
        disconnectPlayer(playerTracker, userId)
    }

    xPosInt, err := strconv.Atoi(xPos)
    if err != nil {
        log.Printf("Invalid xPos parameter: %v\n", err)
        return "", 0, 0, errors.New("xPos is required")
    }

    yPosInt, err := strconv.Atoi(yPos)
    if err != nil {
        log.Printf("Invalid yPos parameter: %v\n", err)
        return "", 0, 0, errors.New("yPos is required")
    }

    return userId, xPosInt, yPosInt, nil
}

func pingLoop(conn *websocket.Conn, writeMu *sync.Mutex, done <-chan struct{}) {
    ticker := time.NewTicker(pingPeriod)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            writeMu.Lock()
            conn.SetWriteDeadline(time.Now().Add(writeWait))
            err := conn.WriteMessage(websocket.PingMessage, nil)
            writeMu.Unlock()
            if err != nil {
                log.Printf("ping: %+v\n", err)
                return
            }
        case <-done:
            return
        }
    }
}

func handler(playerTracker *PlayerTracker,  w http.ResponseWriter, r *http.Request) {
        queryParams := r.URL.Query()
        userId := queryParams.Get("userId")
        xPos := queryParams.Get("xPos")
        yPos := queryParams.Get("yPos")

        userId, xPosInt, yPosInt, err := validate(playerTracker, userId, xPos, yPos)
        if err != nil {
            http.Error(w, err.Error(), http.StatusBadRequest)
            return
        }

        conn, err := upgrader.Upgrade(w, r, nil)
        if err != nil {
            log.Printf("upgrade failed: %+v\n", err)
            http.Error(w, "upgrade failed", http.StatusBadRequest)
            return
        }
        defer conn.Close()

        var playerPos = PlayerPosition{
            XPos:   xPosInt,
            YPos:   yPosInt,
        }

        player := connectPlayer(playerTracker, userId, playerPos, conn)

        conn.SetReadDeadline(time.Now().Add(pongWait)) 
        conn.SetPongHandler(func(string) error { 
            conn.SetReadDeadline(time.Now().Add(pongWait))
            return nil
        })

        player.WriteMu.Lock()
        conn.SetWriteDeadline(time.Now().Add(writeWait))
        err = conn.WriteJSON(Message{Type: playerState, Players: snapshot(playerTracker)})
        player.WriteMu.Unlock()
        if err != nil {
            log.Printf("write: %+v\n", err)
            disconnectPlayer(playerTracker, userId)
            return
        }

        done := make(chan struct{}) 
        defer close(done) 
        go pingLoop(conn, &player.WriteMu, done) 

        for {
            _, _, err := conn.ReadMessage()
            if err != nil {
                log.Printf("Error from client: %+v\n", err)
                disconnectPlayer(playerTracker, userId)
                return
            }

            player.WriteMu.Lock()
            conn.SetWriteDeadline(time.Now().Add(writeWait))
            err = conn.WriteJSON(Message{Type: playerState, Players: snapshot(playerTracker)})
            player.WriteMu.Unlock()
            if err != nil {
                log.Printf("write: %+v\n", err)
                disconnectPlayer(playerTracker, userId)
                return
            }
        }
}

func main() {
    playerTracker := &PlayerTracker{
        players: make(map[string]*connectedPlayer),
    }
    http.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
        handler(playerTracker, w, r)
    })

    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        http.ServeFile(w, r, "websockets.html")
    })

    log.Printf("server is listening on port :8080")
    http.ListenAndServe(":8080", nil)
}