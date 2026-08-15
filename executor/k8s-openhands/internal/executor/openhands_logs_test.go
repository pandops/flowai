package executor

import "testing"

func TestPublishedLogsFromOpenHandsMapsExplicitAgentAndToolOutput(t *testing.T) {
	event := map[string]any{
		"kind": "EventEnvelope",
		"payload": []any{
			map[string]any{"kind": "MessageEvent", "source": "agent", "content": []any{map[string]any{"type": "text", "text": "Проверяю рабочее дерево"}}},
			map[string]any{"kind": "ObservationEvent", "observation": "FlowAI marker written"},
		},
	}
	got := publishedLogsFromOpenHands(event)
	if len(got) != 2 || got[0].stream != "reasoning" || got[0].content != "Проверяю рабочее дерево" ||
		got[1].stream != "work" || got[1].content != "FlowAI marker written" {
		t.Fatalf("published logs = %#v", got)
	}
}

func TestPublishedLogsFromOpenHandsIgnoresStatusAndUserInput(t *testing.T) {
	event := map[string]any{"type": "conversation.status", "execution_status": "finished",
		"message": map[string]any{"role": "user", "content": "private prompt"}}
	if got := publishedLogsFromOpenHands(event); len(got) != 0 {
		t.Fatalf("status/user input must not become logs: %#v", got)
	}
}
