# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Architecture Overview

This is a Go-based transparent proxy system for Claude Code that implements a dual-port microservices architecture:

- **LLM Proxy Server** (port 9999): Handles all LLM API requests (`/v1/*`, `/health`)
- **Admin Server** (port 9998): Provides web management interface (`/`)
- **Server Routing Manager**: Orchestrates both servers' lifecycle
- **Provider System**: Manages multiple LLM API providers with failover and load balancing

The system acts as a transparent proxy between Claude Code and various LLM API providers (like SiliconFlow), supporting model mapping, authentication handling, and intelligent routing.

## Key Components

- `cmd/ccenv/`: Main CLI application using Cobra framework
- `internal/llm_proxy/`: LLM API proxy server with request forwarding
- `internal/admin/`: Web-based admin interface server  
- `internal/server_routing_manager/`: Coordinates dual-server lifecycle
- `internal/provider/`: Provider management and routing strategies
- `internal/config/`: Configuration management with hot-reload via fsnotify
- `internal/executor/`: Core execution logic and service orchestration
- `internal/logger/`: Centralized logging with request tracing

## Development Commands

### Building
```bash
# Build for current platform
./tools/build.sh
# Build for all platforms  
./tools/build.sh all
# Build for specific platform
./tools/build.sh linux|darwin|windows
```

### Testing
```bash
# Run all tests
go test ./...
# Run specific package tests
go test ./internal/config
# Run tests with verbose output
go test -v ./internal/config
```

### Running Locally
```bash
# Build the binary
go build -o ccenv ./cmd/ccenv
# Start proxy service (foreground)
./ccenv start
# Launch Claude Code with proxy
./ccenv code
# View logs
./ccenv logs -f
# Show configuration
./ccenv config
```

## Configuration

The system uses JSON configuration at `~/.claude-code-env/settings.json`:
- Dual port configuration: `LLM_PROXY_PORT` (9999), `ADMIN_PORT` (9998) 
- Provider array with `name`, `state` ("on"/"off"), and `env` map
- Routing strategies: `"default"` (failover) or `"robin"` (round-robin)
- Authentication via `ANTHROPIC_AUTH_TOKEN` (Bearer) or `ANTHROPIC_API_KEY` (X-Api-Key)

Configuration supports hot-reload without service restart.

## Architecture Patterns

### Dual-Port Service Pattern
The system splits functionality across two independent HTTP servers for fault isolation. The ServerRoutingManager coordinates their lifecycle with parallel startup/shutdown.

### Provider Management
Uses a strategy pattern for routing (`default` vs `robin`) with circuit-breaker-like failure detection (5 failures = 5 minute disable). Supports model mapping to transform incoming model names.

### Request Lifecycle
1. CLI receives command → 2. Executor starts services → 3. ServerRoutingManager orchestrates → 4. Provider selects backend → 5. LLM Proxy forwards request → 6. Response streamed back

### Configuration Hot-Reload  
Uses fsnotify to watch config file changes, triggering service restart while preserving active connections. Provider failure counts reset on config reload.

## Code Conventions

- Use underscore naming for packages (`llm_proxy`, `server_routing_manager`) to avoid Go import alias complexity
- Request tracing via unique IDs for debugging
- Centralized error handling with Anthropic API-compatible JSON responses
- Graceful shutdown with context timeouts
- Structured logging with module-specific prefixes