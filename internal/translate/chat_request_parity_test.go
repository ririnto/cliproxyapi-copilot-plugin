package translate

import (
	"context"
	"strings"
	"testing"

	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestChatToResponsesPreservesNativeConstraintsSearchAndToolHistory(t *testing.T) {
	request := []byte(`{"messages":[{"role":"user","content":"look up the value"},{"role":"assistant","tool_calls":[{"id":"call/+preserve","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call/+preserve","content":"found"}],"tools":[{"type":"function","function":{"name":"lookup","description":"Find a value","parameters":{"type":"object"},"strict":false}},{"type":"function","function":{"name":"write","parameters":{"type":"object"},"strict":true}},{"type":"function","function":{"name":"summarize","parameters":{"type":"object"}}}],"response_format":{"type":"json_schema","json_schema":{"name":"answer","description":"A concise answer","strict":false,"schema":{"type":"object","properties":{"answer":{"type":"string"}}}}},"web_search_options":{"search_context_size":"low","user_location":{"type":"approximate","country":"US"}}}`)

	translated, err := RequestForEndpointFrom("openai", "gpt-6-luna", request, false, EndpointResponses)
	if err != nil {
		t.Fatalf("translate Chat request: %v", err)
	}
	if gjson.GetBytes(translated, "text.format.type").String() != "json_schema" || gjson.GetBytes(translated, "text.format.name").String() != "answer" || gjson.GetBytes(translated, "text.format.description").String() != "A concise answer" || !gjson.GetBytes(translated, "text.format.strict").Exists() || gjson.GetBytes(translated, "text.format.strict").Bool() {
		t.Fatalf("Chat response_format changed: %s", translated)
	}
	if gjson.GetBytes(translated, "text.format.schema.properties.answer.type").String() != "string" {
		t.Fatalf("Chat JSON schema was not preserved: %s", translated)
	}
	if len(gjson.GetBytes(translated, "tools").Array()) != 4 {
		t.Fatalf("function tools or hosted search were lost: %s", translated)
	}
	if gjson.GetBytes(translated, "tools.0.strict").Bool() || !gjson.GetBytes(translated, "tools.1.strict").Bool() || gjson.GetBytes(translated, "tools.2.strict").Type != gjson.False {
		t.Fatalf("Chat function strictness changed: %s", translated)
	}
	if strings.Contains(gjson.GetBytes(translated, "instructions").String(), "Schema Name: answer") {
		t.Fatalf("Chat response_format became instruction prose instead of a native constraint: %s", translated)
	}
	if gjson.GetBytes(translated, "tools.3.type").String() != "web_search" || gjson.GetBytes(translated, "tools.3.search_context_size").String() != "low" || gjson.GetBytes(translated, "tools.3.user_location.country").String() != "US" {
		t.Fatalf("Chat hosted search was not preserved natively: %s", translated)
	}
	if strings.Contains(gjson.GetBytes(translated, "tools").Raw, `"name":"web_search"`) {
		t.Fatalf("hosted search became a client function: %s", translated)
	}
	foundCall, foundResult := false, false
	for _, item := range gjson.GetBytes(translated, "input").Array() {
		switch item.Get("type").String() {
		case "function_call":
			foundCall = item.Get("call_id").String() == "call/+preserve" && item.Get("arguments").String() == "{}"
		case "function_call_output":
			foundResult = item.Get("call_id").String() == "call/+preserve" && item.Get("output").String() == "found"
		}
	}
	if !foundCall || !foundResult {
		t.Fatalf("explicit empty arguments or paired tool identity changed: %s", translated)
	}
}

func TestChatToResponsesPreservesTextAndJSONModeFormats(t *testing.T) {
	for _, test := range []struct {
		name   string
		format string
		want   string
	}{
		{name: "text", format: `{"type":"text"}`, want: "text"},
		{name: "json object", format: `{"type":"json_object"}`, want: "json_object"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := []byte(`{"messages":[{"role":"user","content":"respond"}],"response_format":` + test.format + `}`)
			translated, err := RequestForEndpointFrom("openai", "gpt-6-luna", request, false, EndpointResponses)
			if err != nil {
				t.Fatalf("translate Chat response format: %v", err)
			}
			if got := gjson.GetBytes(translated, "text.format.type").String(); got != test.want {
				t.Fatalf("text.format.type = %q, want %q; request=%s", got, test.want, translated)
			}
		})
	}
}

func TestChatToMessagesPreservesZeroTemperatureAndToolIdentity(t *testing.T) {
	request := []byte(`{"temperature":0,"messages":[{"role":"user","content":"lookup"},{"role":"assistant","tool_calls":[{"id":"call/+messages","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call/+messages","content":"found"}]}`)
	translated, err := RequestForEndpointFrom("openai", "claude-sonnet-5.5", request, false, EndpointMessages)
	if err != nil {
		t.Fatalf("translate Chat request to Messages: %v", err)
	}
	if !gjson.GetBytes(translated, "temperature").Exists() || gjson.GetBytes(translated, "temperature").Type != gjson.Number || gjson.GetBytes(translated, "temperature").Float() != 0 {
		t.Fatalf("explicit zero temperature was lost: %s", translated)
	}
	var callID, resultID string
	for _, message := range gjson.GetBytes(translated, "messages").Array() {
		for _, block := range message.Get("content").Array() {
			switch block.Get("type").String() {
			case "tool_use":
				callID = block.Get("id").String()
				if block.Get("input").Raw != `{}` {
					t.Fatalf("explicit empty function arguments changed: %s", translated)
				}
			case "tool_result":
				resultID = block.Get("tool_use_id").String()
			}
		}
	}
	if callID != "call/+messages" || resultID != callID {
		t.Fatalf("Messages tool call/result identity changed: call=%q result=%q request=%s", callID, resultID, translated)
	}
}

func TestChatFunctionHistoryRequiresCompleteObjectCallsForBothDestinations(t *testing.T) {
	base := []byte(`{"messages":[{"role":"user","content":"lookup"},{"role":"assistant","tool_calls":[{"id":"call-valid","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call-valid","content":"found"}]}`)
	for _, test := range []struct {
		name string
		path string
		raw  string
		set  bool
	}{
		{name: "missing call ID", path: "messages.1.tool_calls.0.id", set: false},
		{name: "blank call ID", path: "messages.1.tool_calls.0.id", raw: `"  "`, set: true},
		{name: "missing function name", path: "messages.1.tool_calls.0.function.name", set: false},
		{name: "blank function name", path: "messages.1.tool_calls.0.function.name", raw: `"\t"`, set: true},
		{name: "missing arguments", path: "messages.1.tool_calls.0.function.arguments", set: false},
		{name: "blank arguments", path: "messages.1.tool_calls.0.function.arguments", raw: `"  "`, set: true},
		{name: "malformed arguments", path: "messages.1.tool_calls.0.function.arguments", raw: `"{bad"`, set: true},
		{name: "array arguments", path: "messages.1.tool_calls.0.function.arguments", raw: `"[]"`, set: true},
		{name: "scalar arguments", path: "messages.1.tool_calls.0.function.arguments", raw: `"null"`, set: true},
		{name: "unpaired result", path: "messages.2.tool_call_id", raw: `"missing-call"`, set: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := append([]byte(nil), base...)
			var err error
			if test.set {
				request, err = sjson.SetRawBytes(request, test.path, []byte(test.raw))
			} else {
				request, err = sjson.DeleteBytes(request, test.path)
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, endpoint := range []string{EndpointResponses, EndpointMessages} {
				if translated, err := RequestForEndpointFrom("openai", "gpt-6-luna", request, false, endpoint); err == nil {
					t.Fatalf("%s accepted invalid tool history: %s", endpoint, translated)
				}
			}
		})
	}
}

func TestChatHostedSearchRejectsUnrepresentableOptionsAndMessages(t *testing.T) {
	for _, test := range []struct {
		name   string
		body   string
		to     string
		wantIn string
	}{
		{name: "unknown search option", body: `{"messages":[{"role":"user","content":"search"}],"web_search_options":{"search_context_size":"low","region":"US"}}`, to: EndpointResponses, wantIn: "unsupported option"},
		{name: "invalid context size", body: `{"messages":[{"role":"user","content":"search"}],"web_search_options":{"search_context_size":"large"}}`, to: EndpointResponses, wantIn: "search_context_size"},
		{name: "Messages cannot host Chat search", body: `{"messages":[{"role":"user","content":"search"}],"web_search_options":{"search_context_size":"low"}}`, to: EndpointMessages, wantIn: "cannot be represented"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if translated, err := RequestForEndpointFrom("openai", "gpt-6-luna", []byte(test.body), false, test.to); err == nil || !strings.Contains(err.Error(), test.wantIn) {
				t.Fatalf("search request was not refused precisely: output=%s error=%v", translated, err)
			}
		})
	}
}

func TestChatMessagesRejectsInvalidExplicitTemperature(t *testing.T) {
	for _, raw := range []string{`null`, `"0"`, `true`, `-0.1`, `1.1`, `1e1000`} {
		t.Run(raw, func(t *testing.T) {
			request := []byte(`{"temperature":` + raw + `,"messages":[{"role":"user","content":"hello"}]}`)
			if translated, err := RequestForEndpointFrom("openai", "claude-sonnet-5.5", request, false, EndpointMessages); err == nil {
				t.Fatalf("accepted invalid Chat temperature: %s", translated)
			}
		})
	}
}

func TestChatResponsesRejectsUnsupportedResponseFormats(t *testing.T) {
	for _, raw := range []string{
		`{"type":"grammar","grammar":"x"}`,
		`{"type":"json_schema","json_schema":{"name":"answer","schema":{"type":"object"},"strict":"true"}}`,
		`{"type":"json_schema","json_schema":{"name":"answer","schema":{"type":"object"},"additional_option":true}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			request := []byte(`{"messages":[{"role":"user","content":"answer"}],"response_format":` + raw + `}`)
			if translated, err := RequestForEndpointFrom("openai", "gpt-6-luna", request, false, EndpointResponses); err == nil {
				t.Fatalf("accepted unsupported response format: %s", translated)
			}
		})
	}
}

func TestChatMessagesPropagatesSDKRefusalForAudioTurn(t *testing.T) {
	request := []byte(`{"messages":[{"role":"system","content":"policy"},{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"UklGRg==","format":"wav"}}]},{"role":"user","content":"another valid turn"}]}`)
	envelope := registry.TranslateRequestEnvelope(context.Background(), sdktranslator.FormatOpenAI, sdktranslator.FormatClaude, sdktranslator.RequestEnvelope{
		Format: sdktranslator.FormatOpenAI,
		Model:  "claude-sonnet-5.5",
		Body:   request,
	})
	if envelope.Err == nil {
		t.Fatal("SDK no longer refuses the unsupported audio turn; update the regression to the maintained refusal")
	}
	if translated, err := RequestForEndpointFrom("openai", "claude-sonnet-5.5", request, false, EndpointMessages); err == nil || !strings.Contains(err.Error(), envelope.Err.Error()) {
		t.Fatalf("SDK refusal was not propagated: output=%s error=%v sdk=%v", translated, err, envelope.Err)
	}
}
