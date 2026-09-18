package main

import (
	"errors"
	"testing"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/storage"
)

type queryExecutionStore struct {
	storage.Storage
	executions []*core.ExecutionRecord
	err        error
}

func (s queryExecutionStore) GetExecutions(storage.QueryOptions) ([]*core.ExecutionRecord, error) {
	return s.executions, s.err
}

func TestSummarizeExecutionsFallback(t *testing.T) {
	executions := []*core.ExecutionRecord{
		{Tool: core.ToolHomebrew},
		{Tool: core.ToolHomebrew},
		{Tool: core.ToolNPM},
	}
	store := queryExecutionStore{executions: executions}
	summary, err := summarizeExecutions(store, storage.QueryOptions{})
	if err != nil {
		t.Fatalf("summarizeExecutions failed: %v", err)
	}
	totalMatches := summary.Total == 3
	homebrewMatches := summary.ToolCounts[core.ToolHomebrew] == 2
	npmMatches := summary.ToolCounts[core.ToolNPM] == 1
	summaryMatches := totalMatches && homebrewMatches && npmMatches
	if !summaryMatches {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestSummarizeExecutionsFallbackError(t *testing.T) {
	wantErr := errors.New("query failed")
	store := queryExecutionStore{err: wantErr}
	_, err := summarizeExecutions(store, storage.QueryOptions{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("summarizeExecutions error = %v", err)
	}
}

type statisticsErrorStore struct {
	queryExecutionStore
	statsErr error
}

func (s statisticsErrorStore) Statistics() (*core.StorageStatistics, error) {
	return nil, s.statsErr
}

func TestPrintStatsReturnsStatisticsError(t *testing.T) {
	wantErr := errors.New("statistics unavailable")
	store := statisticsErrorStore{statsErr: wantErr}
	err := printStats(store, statsCommandOptions{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("printStats error = %v, want wrapped %v", err, wantErr)
	}
}

type statsReportErrorStore struct {
	queryExecutionStore
	statsErr    error
	packagesErr error
}

func (s statsReportErrorStore) Statistics() (*core.StorageStatistics, error) {
	return &core.StorageStatistics{}, s.statsErr
}

func (s statsReportErrorStore) GetPackages(string) ([]*core.PackageInfo, error) {
	return nil, s.packagesErr
}

func TestStatsJSONStorageFailuresLeaveOutputEmpty(t *testing.T) {
	wantErr := errors.New("storage unavailable")
	stores := []statsReportErrorStore{
		{queryExecutionStore: queryExecutionStore{err: wantErr}},
		{statsErr: wantErr},
		{packagesErr: wantErr},
	}
	for _, store := range stores {
		assertStatsJSONFailure(t, store, wantErr)
	}
}

func assertStatsJSONFailure(t *testing.T, store storage.Storage, wantErr error) {
	t.Helper()
	var err error
	output := captureStdout(t, func() {
		err = printStats(store, statsCommandOptions{top: 5, format: formatJSON})
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want wrapped %v", err, wantErr)
	}
	if output != "" {
		t.Fatalf("failed stats printed a report: %q", output)
	}
}
