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
