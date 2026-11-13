// Package view holds the response shapes written by v1 handlers.
package view

import "github.com/xingyunqi-tech/go-template/pkg/jsontime"

// AppInfo is the GET / payload.
type AppInfo struct {
	Name string `json:"name"`
}

// HealthCheck is the GET /api/health payload.
type HealthCheck struct {
	Status    string        `json:"status"`
	Timestamp jsontime.Time `json:"timestamp"`
	Uptime    int64         `json:"uptime"`
	Memory    Memory        `json:"memory"`
	Node      string        `json:"node"`
}

// Memory is the process memory in MB.
type Memory struct {
	HeapUsed  int64 `json:"heapUsed"`
	HeapTotal int64 `json:"heapTotal"`
	Rss       int64 `json:"rss"`
}
