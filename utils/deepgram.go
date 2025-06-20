package utils

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"strconv"
	"strings"

	msginterfaces "github.com/deepgram/deepgram-go-sdk/pkg/api/listen/v1/websocket/interfaces"
	"github.com/deepgram/deepgram-go-sdk/pkg/client/interfaces"
	"github.com/deepgram/deepgram-go-sdk/pkg/client/listen"
	"go.uber.org/zap"
)

type DeepgramCallback struct {
	TranscriptionChannel chan string
	confidenceThreshold  float64

	lang                string
	totalAudioBytesSent int64
}

type DeepgramClient struct {
	dgClient *listen.WSCallback
	callback *DeepgramCallback
}

func (c *DeepgramCallback) defaultConfidenceThreshold() float64 {
	return c.confidenceThreshold
}

func InitDeepgramClient(
	lang string,
	confidenceThreshold string,
	transcriptionCh chan string,
) *DeepgramClient {
	apiKey := os.Getenv("DEEPGRAM_API_KEY")

	if apiKey == "" {
		zap.L().Error("DEEPGRAM_API_KEY environment variable not set")
	}

	model := "nova-3"

	ctx := context.Background()
	transcriptOptions := &interfaces.LiveTranscriptionOptions{
		Language:       lang,
		Channels:       1,
		Endpointing:    "100",
		InterimResults: true,
		FillerWords:    true,
		Model:          model,
		UtteranceEndMs: "1500",
	}

	if lang != "en" && model == "nova-3" {
		zap.L().Warn("Using multilingual model for non-English language on Nova 3", zap.String("language", lang))
		transcriptOptions.Language = "multi"
	}

	clientOptions := &interfaces.ClientOptions{
		EnableKeepAlive: true,
	}

	zap.L().Info("Using Deepgram Remote")

	confidenceThresholdFloat, _ := strconv.ParseFloat(confidenceThreshold, 64)
	zap.L().Info("Confidence threshold", zap.Float64("threshold", confidenceThresholdFloat))

	callback := &DeepgramCallback{
		TranscriptionChannel: transcriptionCh,
		confidenceThreshold:  confidenceThresholdFloat,

		lang:                lang,
		totalAudioBytesSent: 0,
	}

	dgClient, err := listen.NewWebSocketUsingCallback(ctx, apiKey, clientOptions, transcriptOptions, callback)
	if err != nil {
		zap.L().Error("ERROR creating LiveTranscription connection", zap.Error(err))
	}

	return &DeepgramClient{
		dgClient: dgClient,
		callback: callback,
	}
}

func (d *DeepgramClient) Connect() {
	if !d.dgClient.Connect() {
		zap.L().Error("ERROR: Failed to connect to Deepgram WebSocket")
	}
}

func (d *DeepgramClient) Send(data []byte) error {
	reader := bufio.NewReader(bytes.NewReader(data))
	err := d.dgClient.Stream(reader)
	if err != nil && err != io.EOF {
		zap.L().Error("Error streaming to Deepgram", zap.Error(err))
		return err
	}
	d.callback.totalAudioBytesSent += int64(len(data))
	return nil
}

func (d *DeepgramClient) Close() {
	d.dgClient.Stop()
}

func (c *DeepgramCallback) Open(or *msginterfaces.OpenResponse) error {
	zap.L().Info("Deepgram socket connection opened")
	return nil
}

func (c *DeepgramCallback) Message(mr *msginterfaces.MessageResponse) error {
	var transcript string
	var transcriptionConfidence float64

	if len(mr.Channel.Alternatives) == 0 {
		zap.L().Warn("No transcription alternatives provided")
		return nil
	}

	alternative := mr.Channel.Alternatives[0]
	transcript = strings.TrimSpace(alternative.Transcript)
	transcriptionConfidence = alternative.Confidence

	if transcript == "" || transcript == " " {
		return nil
	}

	if transcriptionConfidence < c.defaultConfidenceThreshold() {
		zap.L().Debug("Discarding low confidence transcript", zap.String("transcript", transcript))
		return nil
	}

	if mr.IsFinal {
		zap.L().Debug("Final word of a sentence received", zap.String("transcript", transcript))
		c.TranscriptionChannel <- transcript
	} else {
		zap.L().Debug("Interim transcript", zap.String("transcript", transcript))
	}

	return nil
}

func (c *DeepgramCallback) Metadata(md *msginterfaces.MetadataResponse) error {
	zap.L().Debug("Received metadata", zap.Any("metadata", md))
	return nil
}

func (c *DeepgramCallback) SpeechStarted(ssr *msginterfaces.SpeechStartedResponse) error {
	zap.L().Debug("Speech started")
	return nil
}

func (c *DeepgramCallback) UtteranceEnd(ur *msginterfaces.UtteranceEndResponse) error {
	zap.L().Debug("Utterance ended")
	c.TranscriptionChannel <- "<END_OF_SPEECH>"
	return nil
}

func (c *DeepgramCallback) Close(cr *msginterfaces.CloseResponse) error {
	zap.L().Info("WebSocket connection closed")
	return nil
}

func (c *DeepgramCallback) Error(er *msginterfaces.ErrorResponse) error {
	zap.L().Error("WebSocket error", zap.Any("error", er))
	return nil
}

func (c *DeepgramCallback) UnhandledEvent(byData []byte) error {
	zap.L().Warn("Unhandled event", zap.String("data", string(byData)))
	return nil
}
