package provider

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestExecutorRejectsUnsupportedAttachmentsBeforeInference(t *testing.T) {
	for _, stream := range []bool{false, true} {
		host := &reasoningHost{}
		service := reasoningService(host)
		request := ExecuteRequest{StreamID: "output", ExecutorRequest: pluginapi.ExecutorRequest{
			AuthID: "account", Model: "gemini-3.8-flash", SourceFormat: "claude",
			OriginalRequest: []byte(`{"messages":[{"role":"user","content":[{"type":"document","source":{"type":"url","url":"https://media.example.invalid/report.pdf"}}]}]}`),
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
		if !errors.As(err, &status) || status.Code != "unsupported_attachment" || status.HTTPStatus != http.StatusUnprocessableEntity || status.Retryable || len(host.requests) != 0 {
			t.Fatalf("stream = %v, error = %#v, dispatches = %d", stream, err, len(host.requests))
		}
	}
}
