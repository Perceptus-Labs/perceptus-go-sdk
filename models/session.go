package models

import (
	"context"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	SESSION_END = "<SESSION_END>"
)

type RoboSession struct {
	ID                   string
	CurrentContext       context.Context
	CancelCurrentContext context.CancelFunc
	Connection           *websocket.Conn
	RedisClient          *redis.Client
	Logger               *zap.Logger

	// Channels for communication between handlers
	TranscriptionCh chan string
	InterruptionCh  chan string
	VideoAnalysisCh chan string
	IntentionCh     chan IntentionResult

	// Session state
	IsActive     bool
	StartTime    time.Time
	LastActivity time.Time

	// Configuration
	VideoFrequency time.Duration // How often to take pictures
	AudioFrequency time.Duration // How often to process audio (if needed)

	// Current transcript buffer
	CurrentTranscript string
	LastActionTime    time.Time
}

type IntentionResult struct {
	HasClearIntention  bool
	IntentionType      string
	Description        string
	Confidence         float64
	EnvironmentContext string
	TranscriptAnalysis string
	Timestamp          time.Time
}

type EnvironmentContext struct {
	ID          string
	SessionID   string
	Description string
	Objects     []string
	Timestamp   time.Time
	ImageData   []byte // Optional: store the image data
}

type VideoAnalysis struct {
	SessionID          string
	Description        string
	KeyObjects         []string
	EnvironmentSummary string
	Timestamp          time.Time
}

func NewRoboSession(id string, conn *websocket.Conn, redisClient *redis.Client) *RoboSession {
	ctx, cancel := context.WithCancel(context.Background())

	// Create a logger with session ID context
	logger := zap.L().With(zap.String("session_id", id))

	session := &RoboSession{
		ID:                   id,
		CurrentContext:       ctx,
		CancelCurrentContext: cancel,
		Connection:           conn,
		RedisClient:          redisClient,
		Logger:               logger,

		TranscriptionCh: make(chan string, 100),
		InterruptionCh:  make(chan string, 100),
		VideoAnalysisCh: make(chan string, 100),
		IntentionCh:     make(chan IntentionResult, 10),

		IsActive:     true,
		StartTime:    time.Now(),
		LastActivity: time.Now(),

		VideoFrequency: 10 * time.Second,       // Default: take picture every 10 seconds
		AudioFrequency: 100 * time.Millisecond, // Default: process audio continuously

		CurrentTranscript: "",
		LastActionTime:    time.Now(),
	}

	return session
}

func (rs *RoboSession) UpdateContext() {
	rs.CancelCurrentContext()
	rs.CurrentContext, rs.CancelCurrentContext = context.WithCancel(context.Background())
	rs.LastActivity = time.Now()
}

func (rs *RoboSession) Stop() {
	rs.Logger.Info("Stopping session")
	rs.IsActive = false

	// Send SESSION_END to all channels to stop all goroutines
	rs.SendToAllChannels(SESSION_END)

	// Cancel current context
	rs.CancelCurrentContext()

	// Close all channels
	close(rs.TranscriptionCh)
	close(rs.InterruptionCh)
	close(rs.VideoAnalysisCh)
	close(rs.IntentionCh)

	if rs.Connection != nil {
		rs.Connection.Close()
	}
}

func (rs *RoboSession) SendToAllChannels(message string) {
	// Send to all channels that accept strings
	select {
	case rs.TranscriptionCh <- message:
	default:
	}
	select {
	case rs.InterruptionCh <- message:
	default:
	}
	select {
	case rs.VideoAnalysisCh <- message:
	default:
	}
}

func (rs *RoboSession) Close() {
	rs.Stop()
}
