# ![PerceptusLabs Logo](https://github.com/Perceptus-Labs/perceptus-go-sdk/blob/main/public/logo.png?raw=true) Perceptus Go SDK - Persisted Multimodal Context Management

**Real-time perception SDK for robotic agents.**
**Built by [PerceptusLabs](https://perceptuslabs.com)**

Perceptus Go SDK is a **Go-based environment awareness layer** that collects multimodal cues from robotic sessions — including video, audio, and contextual signals — and converts them into structured intentions. These are passed on to the [Intentus](https://github.com/Perceptus-Labs/Intentus) agent for reasoning and orchestration.

> “The bridge between perception and cognition starts with context — and this SDK builds that bridge.”

---

## ✨ Key Capabilities

* **WebSocket-Based Session Interface**
  Initiates robot sessions with full-duplex audio/video streaming and command channels.

* **AI-Enhanced Perception**
  Extracts high-level understanding from frames via GPT-4V, and transcribes audio using Deepgram.

* **Real-Time Intention Detection**
  Converts sensor input into structured JSON intentions to trigger agent reasoning.

* **Context Persistence**
  Caches observations and context vectors with Pinecone and Redis for ongoing agent memory.

* **Production Deployment Ready**
  Dockerized, scriptable, and CI/CD-compatible for deployment across edge or cloud infrastructure.

---

## 📦 Installation (Docker Recommended)

```bash
git clone https://github.com/Perceptus-Labs/perceptus-go-sdk.git
cd perceptus-go-sdk
cp config.example.env .env  # Set your API keys
docker-compose up -d
```

Access the system via:

* API: `http://localhost:8080`
* Example Client: `http://localhost:8080/example_client.html`

---

## 🧠 How It Works

1. **Initiate a session** via WebSocket from your robot or browser.
2. **Stream video/audio** from the environment.
3. **SDK transcribes, analyzes, and classifies** the input in real time.
4. **Constructs an intention payload** for Intentus agent to act on.
5. **Persists relevant cues and vectors** for memory-aware interactions.

---

## 🚀 Developer Quickstart

### Local Dev Setup

```bash
make setup         # Install Go modules and tools
make run           # Start the server
```

### Example Web Client

1. Navigate to: [http://localhost:8080/example\_client.html](http://localhost:8080/example_client.html)
2. Grant microphone & camera access
3. Click “Connect” and start streaming

---

## 🔧 Environment Configuration

`.env` should include:

```env
# Redis
REDIS_URL=redis://localhost:6379

# OpenAI (GPT-4V)
OPENAI_API_KEY=your_openai_key

# Deepgram (Speech to Text)
DEEPGRAM_API_KEY=your_deepgram_key

# Pinecone (Vector DB)
PINECONE_API_KEY=your_pinecone_key
PINECONE_HOST=your_host
PINECONE_NAMESPACE=your_namespace

# Intentus Orchestrator (optional)
ORCHESTRATOR_URL=http://localhost:8000
ORCHESTRATOR_API_KEY=your_intentus_key
```

---

## 🔌 Interfaces

### WebSocket

* `ws://localhost:8080/robot/session` – Handles real-time sessions with robots or browsers

### HTTP

* `GET /health` – Liveness check
* `GET /example_client.html` – Frontend test interface

---

## ✅ Testing

```bash
make test          # Run unit tests
make health        # Check service status
docker-compose logs -f  # Stream server logs
```

---

## 🧱 Use Case Example

A warehouse robot uses the Go SDK to:

1. Stream video frames of its surroundings
2. Detect an obstructed path using GPT-4V
3. Transcribe a human command: “Go around that box”
4. Bundle all into a structured intention
5. Send to Intentus for multi-step reasoning & route planning

---

## 📈 For Investors & Partners

The Perceptus Go SDK enables:

* Seamless multimodal perception for autonomous systems
* Abstracting raw sensor input into actionable, structured thought
* Real-time cognition pipeline from camera/mic → intention → action
* Extendable integrations with new sensors or AI services
* Edge deployment ready via Go + Docker

It’s the **perception bridge** for intelligent agent ecosystems.

---

## 👥 About PerceptusLabs

We are building the nervous system for robotic cognition — enabling autonomous systems to sense, understand, and act with increasing clarity and memory.

Visit [perceptuslabs.com](https://perceptuslabs.com) to learn more.
