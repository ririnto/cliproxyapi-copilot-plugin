package provider

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestExecutorRejectsUnsupportedProviderStateBeforeInference(t *testing.T) {
	for _, test := range []struct{ source, body string }{
		{"claude", `{"messages":[{"role":"assistant","content":[{"type":"server_tool_use","id":"call_a","name":"web_search","input":{"query":"synthetic"}}]}]}`},
		{"claude", `{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"synthetic","signature":"synthetic-signature"}]}]}`},
		{"claude", `{"messages":[{"role":"assistant","content":[{"type":"redacted_thinking","data":"synthetic-redacted"}]}]}`},
		{"openai-response", `{"input":[{"type":"reasoning","summary":[],"encrypted_content":"synthetic-encrypted"}]}`},
	} {
		for _, stream := range []bool{false, true} {
			host := &reasoningHost{}
			service := reasoningService(host)
			request := ExecuteRequest{StreamID: "output", ExecutorRequest: pluginapi.ExecutorRequest{
				AuthID: "account", Model: "gemini-3.8-flash", SourceFormat: test.source,
				OriginalRequest: []byte(test.body),
				Payload:         []byte(`{"input":[{"role":"user","content":"already translated"}]}`),
				StorageJSON:     []byte(`{"type":"copilot","github_access_token":"synthetic-credential"}`),
			}}
			var err error
			if stream {
				_, err = service.ExecuteStream(context.Background(), request)
			} else {
				_, err = service.Execute(context.Background(), request)
			}
			var status *StatusError
			if !errors.As(err, &status) || status.Code != "unsupported_provider_state" || status.HTTPStatus != http.StatusUnprocessableEntity || status.Retryable || len(host.requests) != 0 {
				t.Fatalf("stream = %v, error = %#v, dispatches = %d", stream, err, len(host.requests))
			}
		}
	}
}
