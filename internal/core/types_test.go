package core

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func sampleExecutionRecord() ExecutionRecord {
	return ExecutionRecord{
		ID:               "test-123",
		Tool:             "homebrew",
		Command:          "brew install wget",
		Args:             []string{"install", "wget"},
		Timestamp:        time.Now(),
		Duration:         5 * time.Second,
		ExitCode:         0,
		WorkingDir:       "/tmp",
		User:             "testuser",
		PackagesAffected: []string{"wget"},
	}
}

func TestExecutionRecordJSONUsesDurationMilliseconds(t *testing.T) {
	record := durationExecutionRecord()
	data := marshalExecutionRecord(t, record)
	raw := unmarshalRawRecord(t, data)
	assertDurationMilliseconds(t, raw, 1500)

	decoded := unmarshalExecutionRecord(t, data)
	assertExecutionRecordRoundTrip(t, decoded, record)
	assertInvalidExecutionRecordJSONPreservesReceiver(t, record)
	assertNullExecutionRecordJSONClearsReceiver(t, record)
}

func durationExecutionRecord() ExecutionRecord {
	record := sampleExecutionRecord()
	record.Duration = 1500 * time.Millisecond
	record.Environment = map[string]string{"CI": "true"}
	record.Metadata = map[string]interface{}{"executable": "brew"}
	return record
}

func marshalExecutionRecord(t *testing.T, record ExecutionRecord) []byte {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	return data
}

func unmarshalRawRecord(t *testing.T, data []byte) map[string]interface{} {
	t.Helper()
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw failed: %v", err)
	}
	return raw
}

func assertDurationMilliseconds(t *testing.T, raw map[string]interface{}, expected int64) {
	t.Helper()
	fieldName := "duration_ms"
	if raw[fieldName] != float64(expected) {
		t.Errorf("Expected %s %d, got %v", fieldName, expected, raw[fieldName])
	}
}

func unmarshalExecutionRecord(t *testing.T, data []byte) ExecutionRecord {
	t.Helper()
	var decoded ExecutionRecord
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal record failed: %v", err)
	}
	return decoded
}

func assertExecutionRecordRoundTrip(t *testing.T, decoded, record ExecutionRecord) {
	t.Helper()
	record.Timestamp = record.Timestamp.UTC()
	decoded.Timestamp = decoded.Timestamp.UTC()
	if !reflect.DeepEqual(decoded, record) {
		t.Fatalf("decoded execution = %#v, want %#v", decoded, record)
	}
}

func assertInvalidExecutionRecordJSONPreservesReceiver(t *testing.T, record ExecutionRecord) {
	t.Helper()
	want := record
	data := []byte(`{"tool":"changed","duration_ms":"invalid"}`)
	if err := json.Unmarshal(data, &record); err == nil {
		t.Fatal("invalid duration was accepted")
	}
	if !reflect.DeepEqual(record, want) {
		t.Fatalf("invalid JSON changed receiver: %#v", record)
	}
}

func assertNullExecutionRecordJSONClearsReceiver(t *testing.T, record ExecutionRecord) {
	t.Helper()
	if err := json.Unmarshal([]byte("null"), &record); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(record, ExecutionRecord{}) {
		t.Fatalf("null JSON did not clear receiver: %#v", record)
	}
}

func TestLaunchAttemptRecordJSONOmitsExecutionDetails(t *testing.T) {
	record := launchAttemptExecutionRecord()
	data := marshalExecutionRecord(t, record)
	raw := unmarshalRawRecord(t, data)
	assertLaunchAttemptJSONFields(t, raw)
	decoded := unmarshalExecutionRecord(t, data)
	assertLaunchAttemptRoundTrip(t, decoded, record)
}

func launchAttemptExecutionRecord() ExecutionRecord {
	return ExecutionRecord{
		Kind: ExecutionKindLaunchAttempt, Tool: ToolNPM, Command: "prettier",
		Args: []string{"--check", "src"}, Timestamp: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC),
		Duration: time.Second, ExitCode: 1, WorkingDir: "/repo", User: "user",
		Environment: map[string]string{"CI": "true"}, PackagesAffected: []string{"prettier"},
		Metadata: map[string]interface{}{"executable": "prettier"},
	}
}

func assertLaunchAttemptJSONFields(t *testing.T, raw map[string]interface{}) {
	t.Helper()
	for _, field := range []string{"args", "duration_ms", "exit_code", "working_dir", "user", "environment"} {
		if _, exists := raw[field]; exists {
			t.Errorf("launch attempt JSON includes %q", field)
		}
	}
	kindMatches := raw["kind"] == ExecutionKindLaunchAttempt
	commandMatches := raw["command"] == "prettier"
	identityMatches := kindMatches && commandMatches
	if !identityMatches {
		t.Fatalf("launch attempt identity = kind %v, command %v", raw["kind"], raw["command"])
	}
}

func assertLaunchAttemptRoundTrip(t *testing.T, decoded, record ExecutionRecord) {
	t.Helper()
	want := ExecutionRecord{
		Kind: record.Kind, Tool: record.Tool, Command: record.Command,
		Timestamp: record.Timestamp, PackagesAffected: record.PackagesAffected, Metadata: record.Metadata,
	}
	if !reflect.DeepEqual(decoded, want) {
		t.Fatalf("decoded launch attempt = %#v, want %#v", decoded, want)
	}
}
