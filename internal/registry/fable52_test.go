package registry

import (
	"reflect"
	"testing"
)

// Specs: https://platform.claude.com/docs/en/models/fable-5-2/migration-guide
func TestClaudeFable52Catalog(t *testing.T) {
	var model *ModelInfo
	for _, candidate := range GetClaudeModels() {
		if candidate.ID == "claude-fable-5-2" {
			if model != nil {
				t.Fatal("duplicate Fable 5.2 catalog entry")
			}
			model = candidate
		}
	}
	if model == nil {
		t.Fatal("Fable 5.2 missing from Claude model catalog")
	}
	if model.DisplayName != "Claude Fable 5.2" || model.OwnedBy != "anthropic" || model.Type != "claude" {
		t.Fatalf("unexpected model identity: %+v", model)
	}
	if model.ContextLength != 1000000 || model.MaxCompletionTokens != 128000 {
		t.Fatalf("unexpected token limits: %+v", model)
	}
	if model.Thinking == nil || model.Thinking.ZeroAllowed || !model.Thinking.DynamicAllowed {
		t.Fatalf("expected always-on adaptive thinking, got %+v", model.Thinking)
	}
	if want := []string{"low", "medium", "high", "xhigh", "max"}; !reflect.DeepEqual(model.Thinking.Levels, want) {
		t.Fatalf("effort levels = %v, want %v", model.Thinking.Levels, want)
	}
	if LookupStaticModelInfo("claude-fable-5-1") == nil {
		t.Fatal("adding Fable 5.2 must retain Fable 5.1")
	}
}
