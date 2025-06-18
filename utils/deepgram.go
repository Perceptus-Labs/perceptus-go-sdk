package utils

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"

	msginterfaces "github.com/deepgram/deepgram-go-sdk/pkg/api/listen/v1/websocket/interfaces"
	"github.com/deepgram/deepgram-go-sdk/pkg/client/interfaces"
	"github.com/deepgram/deepgram-go-sdk/pkg/client/listen"
)

type DeepgramCallback struct {
	TranscriptionChannel    chan string
	InterruptionChannel     chan string
	useDeepgramUtteranceEnd bool
	confidenceThreshold     float64

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
	useDeepgramNova3 bool,
	useDeepgramUtteranceEnd bool,
	utteranceEndThreshold int,
	confidenceThreshold string,
	transcriptionCh,
	interruptionCh chan string,
) *DeepgramClient {
	apiKey := os.Getenv("DEEPGRAM_API_KEY")

	if apiKey == "" {
		log.Error("DEEPGRAM_API_KEY environment variable not set")
	}

	model := "nova-3"
	if !useDeepgramNova3 {
		model = "nova-2"
	}

	ctx := context.Background()
	transcriptOptions := &interfaces.LiveTranscriptionOptions{
		Language:       lang,
		Encoding:       "mulaw",
		SampleRate:     8000,
		Channels:       1,
		Endpointing:    "100",
		InterimResults: true,
		FillerWords:    true,
		Model:          model,
	}

	if lang != "en" && model == "nova-3" {
		log.Warn("Using multilingual model for non-English language on Nova 3:", lang)
		transcriptOptions.Language = "multi"
	}

	if useDeepgramUtteranceEnd {
		transcriptOptions.UtteranceEndMs = strconv.Itoa(utteranceEndThreshold)
	}

	clientOptions := &interfaces.ClientOptions{
		EnableKeepAlive: true,
	}

	log.Info("Using Deepgram Remote")

	confidenceThresholdFloat, _ := strconv.ParseFloat(confidenceThreshold, 64)
	log.Info("Confidence threshold:", confidenceThresholdFloat)

	callback := &DeepgramCallback{
		TranscriptionChannel:    transcriptionCh,
		InterruptionChannel:     interruptionCh,
		useDeepgramUtteranceEnd: useDeepgramUtteranceEnd,
		confidenceThreshold:     confidenceThresholdFloat,

		lang:                lang,
		totalAudioBytesSent: 0,
	}

	dgClient, err := listen.NewWebSocketUsingCallback(ctx, apiKey, clientOptions, transcriptOptions, callback)
	if err != nil {
		log.Error("ERROR creating LiveTranscription connection:", err)
	}

	return &DeepgramClient{
		dgClient: dgClient,
		callback: callback,
	}
}

func (d *DeepgramClient) Connect() {
	if !d.dgClient.Connect() {
		log.Error("ERROR: Failed to connect to Deepgram WebSocket")
	}
}

func (d *DeepgramClient) Send(data []byte) error {
	reader := bufio.NewReader(bytes.NewReader(data))
	err := d.dgClient.Stream(reader)
	if err != nil && err != io.EOF {
		log.Error("Error streaming to Deepgram:", err)
		return err
	}
	d.callback.totalAudioBytesSent += int64(len(data))
	return nil
}

func (d *DeepgramClient) Close() {
	d.dgClient.Stop()
}

func (c *DeepgramCallback) Open(or *msginterfaces.OpenResponse) error {
	log.Info("Deepgram socket connection opened")
	return nil
}

func (c *DeepgramCallback) Message(mr *msginterfaces.MessageResponse) error {
	var transcript string
	var transcriptionConfidence float64

	if len(mr.Channel.Alternatives) == 0 {
		log.Warn("No transcription alternatives provided")
		return nil
	}

	alternative := mr.Channel.Alternatives[0]
	transcript = strings.TrimSpace(alternative.Transcript)
	transcriptionConfidence = alternative.Confidence

	if transcript == "" || transcript == " " {
		return nil
	}

	if transcriptionConfidence < c.defaultConfidenceThreshold() {
		log.Debug("Discarding low confidence transcript:", transcript)
		return nil
	}

	c.InterruptionChannel <- transcript
	if mr.IsFinal {
		log.Debug("Final word of a sentence received:", transcript)
		c.TranscriptionChannel <- transcript
	} else {
		log.Debug("Interim transcript:", transcript)
	}

	if !c.useDeepgramUtteranceEnd && mr.SpeechFinal {
		log.Debug("Speech final")
		c.TranscriptionChannel <- "<END_OF_SPEECH>"
	}

	return nil
}

func (c *DeepgramCallback) Metadata(md *msginterfaces.MetadataResponse) error {
	log.Debug("Received metadata:", md)
	return nil
}

func (c *DeepgramCallback) SpeechStarted(ssr *msginterfaces.SpeechStartedResponse) error {
	log.Debug("Speech started")
	return nil
}

func (c *DeepgramCallback) UtteranceEnd(ur *msginterfaces.UtteranceEndResponse) error {
	log.Debug("Utterance ended")
	c.TranscriptionChannel <- "<END_OF_SPEECH>"
	return nil
}

func (c *DeepgramCallback) Close(cr *msginterfaces.CloseResponse) error {
	log.Info("WebSocket connection closed")
	return nil
}

func (c *DeepgramCallback) Error(er *msginterfaces.ErrorResponse) error {
	log.Error("WebSocket error:", er)
	return nil
}

func (c *DeepgramCallback) UnhandledEvent(byData []byte) error {
	log.Warn("Unhandled event:", string(byData))
	return nil
}
