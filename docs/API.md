# DIU API Documentation

## Overview

The DIU daemon exposes a RESTful API for interacting with execution data, package information, and statistics.

**Base URL:** `http://localhost:8081/api/v1`

## Authentication

Currently, the API does not require authentication. Future versions may include token-based authentication.

## Endpoints

### Health Check

Check the health status of the DIU daemon.

**Endpoint:** `GET /health`

**Response:**
```json
{
  "status": "healthy",
  "version": "0.1.0",
  "uptime": "2h15m30s",
  "last_execution": "2025-09-15T10:30:00Z",
  "monitors_active": ["homebrew", "npm", "go"]
}
```

### Executions

#### List Executions

Get a list of tracked executions.

**Endpoint:** `GET /executions`

**Query Parameters:**
- `tool` (string): Filter by tool name
- `package` (string): Filter by package name
- `since` (string): ISO 8601 timestamp
- `until` (string): ISO 8601 timestamp
- `limit` (integer): Maximum number of results (default: 100)
- `offset` (integer): Pagination offset

**Example Request:**
```
GET /api/v1/executions?tool=homebrew&limit=10
```

**Response:**
```json
[
  {
    "id": "exec_20250915_103000_abc123",
    "tool": "homebrew",
    "command": "brew install wget",
    "args": ["install", "wget"],
    "timestamp": "2025-09-15T10:30:00Z",
    "duration_ms": 45230,
    "exit_code": 0,
    "working_dir": "/Users/john/projects",
    "user": "john",
    "packages_affected": ["wget"],
    "metadata": {
      "brew_version": "4.1.15",
      "formulae_updated": ["wget"]
    }
  }
]
```

#### Get Execution by ID

Get details of a specific execution.

**Endpoint:** `GET /executions/{id}`

**Response:** Single execution object

#### Add Execution

Record a new execution (used by wrappers and monitors).

**Endpoint:** `POST /executions`

**Request Body:**
```json
{
  "tool": "npm",
  "command": "npm install express",
  "args": ["install", "express"],
  "exit_code": 0,
  "duration_ms": 5432,
  "timestamp": "2025-09-15T10:35:00Z",
  "working_dir": "/Users/john/myapp",
  "user": "john",
  "packages_affected": ["express"],
  "environment": {
    "NODE_ENV": "development"
  },
  "metadata": {
    "npm_version": "9.8.1"
  }
}
```

**Response:**
- `202 Accepted` - Execution queued for processing
- `400 Bad Request` - Invalid request data
- `503 Service Unavailable` - Event queue full

### Packages

#### List Packages

Get a list of tracked packages.

**Endpoint:** `GET /packages`

**Query Parameters:**
- `tool` (string): Filter by tool name
- `unused_since` (string): ISO 8601 timestamp
- `sort` (string): Sort field (name, last_used, usage_count)
- `order` (string): Sort order (asc, desc)

**Response:**
```json
[
  {
    "name": "wget",
    "version": "1.21.3",
    "tool": "homebrew",
    "install_date": "2025-09-01T12:00:00Z",
    "last_used": "2025-09-15T10:30:00Z",
    "usage_count": 15,
    "path": "/usr/local/bin/wget",
    "dependencies": []
  }
]
```

#### Get Package Details

Get details of a specific package.

**Endpoint:** `GET /packages/{tool}/{name}`

**Response:** Single package object

### Statistics

#### Get Statistics

Get usage statistics.

**Endpoint:** `GET /stats`

**Query Parameters:**
- `period` (string): Time period (daily, weekly, monthly)
- `tool` (string): Filter by tool
- `from` (string): Start date (ISO 8601)
- `to` (string): End date (ISO 8601)

**Response:**
```json
{
  "total_executions": 1523,
  "tools_used": ["homebrew", "npm", "go"],
  "most_active_day": "2025-09-14",
  "execution_frequency": {
    "homebrew": 523,
    "npm": 876,
    "go": 124
  },
  "top_packages": [
    {
      "name": "webpack",
      "tool": "npm",
      "usage_count": 234
    }
  ],
  "daily_breakdown": [
    {
      "date": "2025-09-15",
      "executions": 45,
      "tools": {
        "homebrew": 12,
        "npm": 33
      }
    }
  ]
}
```

### Tools

#### List Monitored Tools

Get a list of currently monitored tools.

**Endpoint:** `GET /tools`

**Response:**
```json
[
  {
    "name": "homebrew",
    "enabled": true,
    "monitor_type": "process",
    "packages_tracked": 156
  },
  {
    "name": "npm",
    "enabled": true,
    "monitor_type": "process",
    "packages_tracked": 423
  }
]
```

## Error Responses

All endpoints use standard HTTP status codes and return error details in JSON format:

```json
{
  "error": {
    "code": "RESOURCE_NOT_FOUND",
    "message": "Execution not found",
    "details": {
      "id": "exec_20250915_999999_xyz"
    }
  }
}
```

### Common Status Codes

- `200 OK` - Request successful
- `202 Accepted` - Request accepted for processing
- `400 Bad Request` - Invalid request parameters
- `404 Not Found` - Resource not found
- `500 Internal Server Error` - Server error
- `503 Service Unavailable` - Service temporarily unavailable

## WebSocket Support (Future)

Future versions will support WebSocket connections for real-time execution updates:

**Endpoint:** `ws://localhost:8081/ws`

**Message Format:**
```json
{
  "type": "execution",
  "data": {
    "tool": "npm",
    "command": "npm install",
    "timestamp": "2025-09-15T10:40:00Z"
  }
}
```

## Rate Limiting

Currently no rate limiting is implemented. Future versions may include:
- 1000 requests per minute per IP
- 100 POST requests per minute per IP

## Versioning

The API uses URL versioning. The current version is `v1`. Future versions will be available at `/api/v2`, etc.

## Examples

### cURL Examples

```bash
# Get health status
curl http://localhost:8081/api/v1/health

# List recent homebrew executions
curl "http://localhost:8081/api/v1/executions?tool=homebrew&limit=10"

# Get unused packages
curl "http://localhost:8081/api/v1/packages?unused_since=2025-06-01T00:00:00Z"

# Get statistics
curl http://localhost:8081/api/v1/stats

# Record an execution
curl -X POST http://localhost:8081/api/v1/executions \
  -H "Content-Type: application/json" \
  -d '{
    "tool": "npm",
    "command": "npm install express",
    "args": ["install", "express"],
    "exit_code": 0,
    "duration_ms": 5432,
    "timestamp": "2025-09-15T10:35:00Z",
    "working_dir": "/tmp",
    "user": "test"
  }'
```

### JavaScript/Node.js Example

```javascript
const axios = require('axios');

const DIU_API = 'http://localhost:8081/api/v1';

// Get recent executions
async function getRecentExecutions(tool, limit = 10) {
  const response = await axios.get(`${DIU_API}/executions`, {
    params: { tool, limit }
  });
  return response.data;
}

// Record an execution
async function recordExecution(executionData) {
  const response = await axios.post(`${DIU_API}/executions`, executionData);
  return response.status === 202;
}

// Get statistics
async function getStatistics() {
  const response = await axios.get(`${DIU_API}/stats`);
  return response.data;
}
```

### Python Example

```python
import requests
from datetime import datetime

DIU_API = "http://localhost:8081/api/v1"

def get_executions(tool=None, limit=10):
    """Get recent executions"""
    params = {"limit": limit}
    if tool:
        params["tool"] = tool

    response = requests.get(f"{DIU_API}/executions", params=params)
    return response.json()

def record_execution(tool, command, args, exit_code, duration_ms):
    """Record a new execution"""
    data = {
        "tool": tool,
        "command": command,
        "args": args,
        "exit_code": exit_code,
        "duration_ms": duration_ms,
        "timestamp": datetime.now().isoformat(),
        "user": os.environ.get("USER")
    }

    response = requests.post(f"{DIU_API}/executions", json=data)
    return response.status_code == 202

def get_unused_packages(days=180):
    """Get packages unused for specified days"""
    cutoff = (datetime.now() - timedelta(days=days)).isoformat()
    response = requests.get(f"{DIU_API}/packages", params={"unused_since": cutoff})
    return response.json()
```