# Simple Go Load Balancer implementation in Go

## Features

- Round-robin load balancing
- Skips unhealthy backends automatically
- Active health checks (on request failure)

## Architecture Overview

- **Reverse Proxy**  
  Uses `net/http/httputil.ReverseProxy` to forward requests to backend servers.

- **Server Pool**  
  Maintains a list of backends and selects the next healthy one using atomic round-robin indexing.

- **Health Checks**
  - *Active*: Marks backend as down after repeated request failures
  - *Passive*: Periodically checks backend reachability using TCP connections


## Running the Load Balancer

### 1. Start some backend servers

Example:
```bash
go run backend.go -port 8080
go run backend.go -port 8081
```

### 2. Run the load balancer

```bash
go run main.go -port 3030 -backends "http://localhost:8080,http://localhost:8081"
```

### 3. Send requests

```bash
curl http://localhost:3030
```

Requests will be distributed across healthy backends.

## Configuration Flags

| `-port` | Load balancer listen port | `3030` |
| `-backends` | Comma-separated backend URLs | `http://localhost:8080,http://localhost:8081` |

## Failure Handling

- Each request retries a backend up to 3 times
- After retries fail, backend is marked down
- Request is retried with another backend 
- If all backends fail, 503 Service Unavailable is returned

## Possible Improvements

- Weighted round-robin or least-connections strategy
- Config file (YAML / JSON)
- HTTPS support


