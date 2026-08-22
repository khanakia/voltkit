package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, b)
	}
	return got
}

func TestJSON_EnvelopeShape(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, "note.list", []string{"a", "b"}, 2); err != nil {
		t.Fatalf("JSON: %v", err)
	}

	got := decode(t, buf.Bytes())
	if got["schema_version"] != float64(SchemaVersion) {
		t.Errorf("schema_version = %v, want %d", got["schema_version"], SchemaVersion)
	}
	if got["kind"] != "note.list" {
		t.Errorf("kind = %v, want %q", got["kind"], "note.list")
	}
	if got["count"] != float64(2) {
		t.Errorf("count = %v, want 2", got["count"])
	}
}

// TestJSON_NilDataBecomesEmptyList pins the contract that breaks the most
// downstream scripts: `"data": null` must never be emitted.
func TestJSON_NilDataBecomesEmptyList(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, "note.list", nil, 0); err != nil {
		t.Fatalf("JSON: %v", err)
	}

	if strings.Contains(buf.String(), "null") {
		t.Errorf("envelope contains null:\n%s", buf.String())
	}
	data, ok := decode(t, buf.Bytes())["data"].([]any)
	if !ok {
		t.Fatalf("data is not a list:\n%s", buf.String())
	}
	if len(data) != 0 {
		t.Errorf("data = %v, want empty list", data)
	}
}

// TestJSON_ZeroCountOmitted covers why count is omitempty: a single-object
// result must not claim a count of zero.
func TestJSON_ZeroCountOmitted(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, "version.show", map[string]string{"version": "v1"}, 0); err != nil {
		t.Fatalf("JSON: %v", err)
	}

	if _, present := decode(t, buf.Bytes())["count"]; present {
		t.Errorf("count present for a single result:\n%s", buf.String())
	}
}

// TestJSON_UnmarshalableDataReturnsError proves a marshal failure surfaces as an
// error instead of writing a truncated envelope to stdout.
func TestJSON_UnmarshalableDataReturnsError(t *testing.T) {
	var buf bytes.Buffer
	err := JSON(&buf, "bad.kind", func() {}, 0)

	if err == nil {
		t.Fatal("expected an error for an unmarshalable payload, got nil")
	}
	if !strings.Contains(err.Error(), "bad.kind") {
		t.Errorf("error does not name the kind: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote output despite the failure: %q", buf.String())
	}
}

func TestJSON_EndsWithNewline(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, "note.show", map[string]string{"id": "1"}, 0); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Error("envelope must end with a newline so it composes with line-oriented tools")
	}
}
