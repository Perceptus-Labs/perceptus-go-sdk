# Perceptus Go SDK

A Go-based SDK for real-time context management and multimedia processing using audio and video inputs.

## Features

- Real-time audio processing using Deepgram
- Video analysis using GPT-4 Vision
- WebSocket-based communication
- Local device integration (microphone and camera)
- Context management system

## Prerequisites

- Go 1.21 or higher
- OpenCV (for video processing)
- Deepgram API key
- OpenAI API key

## Installation

```bash
go get github.com/haohanwang/perceptus-go-sdk
```

## Project Structure

```
.
├── cmd/
│   └── main.go           # Main application entry point
├── internal/
│   ├── audio/           # Audio processing components
│   ├── video/           # Video processing components
│   ├── context/         # Context management system
│   └── websocket/       # WebSocket handlers
├── pkg/                 # Public API packages
└── config/             # Configuration files
```

## Usage

[Documentation coming soon]

## License

MIT 