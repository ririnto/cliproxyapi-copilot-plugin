package translate

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestRequestRoutingPreservesExactNumbers(t *testing.T) {
	for _, test := range []struct {
		name, source, endpoint, body, path string
	}{
		{"Messages tool input", "claude", EndpointMessages, `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_a","name":"inspect","input":{"value":9007199254740993}}]}]}`, "messages.0.content.0.input.value"},
		{"Responses schema", "openai-response", EndpointResponses, `{"input":"inspect","tools":[{"type":"function","name":"inspect","parameters":{"minimum":9007199254740993}}]}`, "tools.0.parameters.minimum"},
		{"Messages to Responses schema", "claude", EndpointResponses, `{"messages":[],"tools":[{"name":"inspect","input_schema":{"minimum":9007199254740993}}]}`, "tools.0.parameters.minimum"},
	} {
		t.Run(test.name, func(t *testing.T) {
			out, err := RequestForEndpointFrom(test.source, "selected-model", []byte(test.body), true, test.endpoint)
			if err != nil {
				t.Fatal(err)
			}
			value := gjson.GetBytes(out, test.path)
			if value.Type != gjson.Number || value.Raw != "9007199254740993" || gjson.GetBytes(out, "model").String() != "selected-model" || !gjson.GetBytes(out, "stream").Bool() {
				t.Fatalf("request fields = %s", out)
			}
		})
	}
}

func TestNativeRequestRejectsNonObjectRoots(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `1`, `{} {}`, `{}x`} {
		out, err := RequestForEndpointFrom("openai-response", "selected-model", []byte(body), false, EndpointResponses)
		if err == nil || out != nil {
			t.Fatalf("payload present = %v, error present = %v", out != nil, err != nil)
		}
	}
}
