package models

import (
	"time"
)

type ExecutionSummary struct {
	Tool       string        `json:"tool"`
	Package    string        `json:"package"`
	Count      int           `json:"count"`
	LastUsed   time.Time     `json:"last_used"`
	TotalTime  time.Duration `json:"total_time_ms"`
}

type DailyStats struct {
	Date       string         `json:"date"`
	Executions int            `json:"executions"`
	Tools      map[string]int `json:"tools"`
	TopPackages []string      `json:"top_packages"`
}

type WeeklyStats struct {
	StartDate   string         `json:"start_date"`
	EndDate     string         `json:"end_date"`
	Executions  int            `json:"executions"`
	Tools       map[string]int `json:"tools"`
	TopPackages []PackageStat  `json:"top_packages"`
}

type PackageStat struct {
	Name       string `json:"name"`
	Tool       string `json:"tool"`
	UsageCount int    `json:"usage_count"`
}

type HealthStatus struct {
	Status      string    `json:"status"`
	Version     string    `json:"version"`
	Uptime      string    `json:"uptime"`
	LastExecution time.Time `json:"last_execution,omitempty"`
	MonitorsActive []string `json:"monitors_active"`
}