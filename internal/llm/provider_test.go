package llm

import (
	"encoding/json"
	"testing"
)

func TestNewWithoutKeyIsNilInterface(t *testing.T) {
	// The pipeline checks `fixer == nil`, so a typed nil pointer wrapped in the
	// interface would silently defeat that guard and panic later.
	fixer, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if fixer != nil {
		t.Fatalf("want an untyped nil interface, got %#v", fixer)
	}
}

func TestNewSelectsProvider(t *testing.T) {
	tests := []struct {
		name      string
		cfg       Config
		wantModel string
		wantErr   bool
	}{
		{"defaults to anthropic", Config{APIKey: "k"}, DefaultAnthropicModel, false},
		{"anthropic explicit", Config{Provider: "anthropic", APIKey: "k"}, DefaultAnthropicModel, false},
		{"gemini explicit", Config{Provider: "gemini", APIKey: "k"}, DefaultGeminiModel, false},
		{"case and space insensitive", Config{Provider: "  GEMINI ", APIKey: "k"}, DefaultGeminiModel, false},
		{"model override", Config{Provider: "gemini", APIKey: "k", Model: "gemini-2.5-pro"}, "gemini-2.5-pro", false},
		{"unknown provider", Config{Provider: "openai", APIKey: "k"}, "", true},
		{"unknown effort", Config{APIKey: "k", Effort: "turbo"}, "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixer, err := New(tc.cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if !fixer.Enabled() {
				t.Error("want Enabled() == true")
			}
			if got := fixer.Model(); got != tc.wantModel {
				t.Errorf("Model() = %q, want %q", got, tc.wantModel)
			}
		})
	}
}

// TestEffortAcceptedByBothProviders guards the mapping tables: every effort the
// config documents must construct on either provider, so a value that works on
// one is never rejected by the other.
func TestEffortAcceptedByBothProviders(t *testing.T) {
	for _, provider := range []string{"anthropic", "gemini"} {
		for _, effort := range []string{"", "low", "medium", "high", "xhigh", "max", "MAX"} {
			if _, err := New(Config{Provider: provider, APIKey: "k", Effort: effort}); err != nil {
				t.Errorf("provider %s, effort %q: %v", provider, effort, err)
			}
		}
	}
}

// TestFixToolSchemaIsWellFormed checks the one definition both providers send.
func TestFixToolSchemaIsWellFormed(t *testing.T) {
	schema := fixToolJSONSchema()

	if _, err := json.Marshal(schema); err != nil {
		t.Fatalf("schema is not JSON-serialisable: %v", err)
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema has no properties object")
	}
	// Every required field must exist as a property, or a strict-mode request is
	// rejected by the API rather than by anything we can see in review.
	for _, name := range fixToolRequired() {
		if _, ok := props[name]; !ok {
			t.Errorf("required field %q has no property definition", name)
		}
	}
	if len(props) != len(fixToolRequired()) {
		t.Errorf("got %d properties for %d required fields; every field is meant to be required",
			len(props), len(fixToolRequired()))
	}
	if schema["additionalProperties"] != false {
		t.Error("additionalProperties must be false so a stray key is rejected")
	}
}

// TestFixCallDecodesToolPayload covers the shared decode path: both providers
// funnel their tool arguments through fixCall, so the struct tags must match the
// schema's field names exactly.
func TestFixCallDecodesToolPayload(t *testing.T) {
	raw := `{
		"root_cause": "nil customer on the refund path",
		"summary": "guard against nil customer",
		"confidence": 0.82,
		"edits": [{
			"path": "billing/refund.py",
			"old_string": "    total = customer.balance",
			"new_string": "    if customer is None:\n        return None\n    total = customer.balance",
			"explanation": "return early when the customer was not found"
		}],
		"no_fix_reason": ""
	}`

	var fc fixCall
	if err := json.Unmarshal([]byte(raw), &fc); err != nil {
		t.Fatalf("decode: %v", err)
	}

	fix := &Fix{Model: "test-model"}
	fc.apply(fix)

	if !fix.Proposed() {
		t.Fatal("want Proposed() == true for a payload carrying one edit")
	}
	if fix.Confidence != 0.82 {
		t.Errorf("Confidence = %v, want 0.82", fix.Confidence)
	}
	if fix.RootCause != "nil customer on the refund path" {
		t.Errorf("RootCause = %q", fix.RootCause)
	}
	if got := fix.Edits[0].Path; got != "billing/refund.py" {
		t.Errorf("edit path = %q", got)
	}
	if fix.Model != "test-model" {
		t.Error("apply must not clobber fields it does not own")
	}
}

// TestFixCallDeclineHasNoEdits records that a decline is a valid outcome, not an
// error — the incident still reaches a human with the analysis attached.
func TestFixCallDeclineHasNoEdits(t *testing.T) {
	var fc fixCall
	raw := `{"root_cause":"upstream timeout","summary":"","confidence":0.2,"edits":[],"no_fix_reason":"cause is outside the provided files"}`
	if err := json.Unmarshal([]byte(raw), &fc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	fix := &Fix{}
	fc.apply(fix)

	if fix.Proposed() {
		t.Error("want Proposed() == false when the model declined")
	}
	if fix.NoFixReason == "" {
		t.Error("a decline must carry a reason")
	}
	if fix.RootCause == "" {
		t.Error("a decline must still carry the root-cause analysis")
	}
}
