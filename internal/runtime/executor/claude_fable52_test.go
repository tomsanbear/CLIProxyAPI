package executor

import (
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestClaudeFable52ModelCapabilities(t *testing.T) {
	for _, model := range []string{
		"claude-fable-5-2",
		"claude-fable-5-2[1m]",
		"claude-fable-5-2-20261109",
		"claude-fable-5.2",
		"provider/claude-fable-5-2",
		" CLAUDE-FABLE-5-2 ",
	} {
		t.Run(model, func(t *testing.T) {
			if !isClaudeFable52Model(model) || !isClaudePreservedThinkingModel(model) || !claudeModelUsesProgressDisplay(model) || !claudeModelHasPerTurnEffort(model) || !claudeModelHasPerTurnTiming(model) {
				t.Fatalf("Fable 5.2 capabilities not recognized for %q", model)
			}
		})
	}
	for _, model := range []string{"claude-fable-5", "claude-fable-5-1", "claude-fable-5-20", "claude-fable-5.20", "claude-mythos-5-2", "claude-fable-5-2unknown"} {
		if isClaudeFable52Model(model) {
			t.Errorf("incorrectly recognized %q as Fable 5.2", model)
		}
	}
}

// Fable 5.2 enforces prefix binding for all accounts, so both execution paths
// must carry the existing replay compatibility setting and its beta header.
func TestClaudeExecutor_Fable52PreservedThinking(t *testing.T) {
	const model = "claude-fable-5-2"
	for _, stream := range []bool{false, true} {
		for _, omitted := range []bool{false, true} {
			name := "execute"
			if stream {
				name = "stream"
			}
			if omitted {
				name += "/implicit-thinking"
			}
			t.Run(name, func(t *testing.T) {
				doc := ojson.NewDoc(preservedThinkingReplayPayload(model))
				if omitted {
					if err := doc.Delete("thinking"); err != nil {
						t.Fatal(err)
					}
				}
				body, headers := runClaudeExecutorCapture(t, nil, "", stream, model, doc.Bytes())
				if got := ojson.GetBytes(body, "model").String(); got != model {
					t.Fatalf("model = %q, want %q", got, model)
				}
				if got := ojson.GetBytes(body, "thinking.type").String(); got != "adaptive" {
					t.Fatalf("thinking.type = %q, want adaptive", got)
				}
				if got := ojson.GetBytes(body, "thinking.block_binding.prefix_mismatch_behavior").String(); got != "drop_block" {
					t.Fatalf("prefix mismatch behavior = %q, want drop_block", got)
				}
				if !strings.Contains(headers.Get("Anthropic-Beta"), claudeThinkingBindingBeta) {
					t.Fatalf("missing binding beta: %v", headers)
				}
				if !strings.Contains(string(body), preservedThinkingTestSignature) {
					t.Fatal("replayed signature was lost")
				}
			})
		}
	}
}

func TestClaudeExecutor_Fable52PayloadRulesRemainFinal(t *testing.T) {
	const model = "claude-fable-5-2"
	for _, stream := range []bool{false, true} {
		for _, filter := range []bool{false, true} {
			cfg := &config.Config{}
			if filter {
				cfg.Payload.Filter = []config.PayloadFilterRule{{
					Models: []config.PayloadModelRule{{Name: model}},
					Params: []string{"thinking.block_binding"},
				}}
			} else {
				cfg.Payload.Override = []config.PayloadRule{{
					Models: []config.PayloadModelRule{{Name: model}},
					Params: map[string]any{"thinking.block_binding.prefix_mismatch_behavior": "error"},
				}}
			}
			body, _ := runClaudeExecutorCapture(t, cfg, "", stream, model, preservedThinkingReplayPayload(model))
			if filter {
				if ojson.GetBytes(body, "thinking.block_binding").Exists() {
					t.Fatalf("payload filter undone (stream=%v): %s", stream, body)
				}
			} else if got := ojson.GetBytes(body, "thinking.block_binding.prefix_mismatch_behavior").String(); got != "error" {
				t.Fatalf("payload override undone (stream=%v): %s", stream, body)
			}
		}
	}
}
