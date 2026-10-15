package provider

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/arthur-sommer-etc/cliproxyapi-copilot-plugin/internal/translate"
	"github.com/arthur-sommer-etc/cliproxyapi-copilot-plugin/internal/transport"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type nativeBetaHost struct {
	inferenceHeaderHost
	done chan struct{}
}

func (h *nativeBetaHost) ReadStream(context.Context, string) (transport.StreamChunk, error) {
	return transport.StreamChunk{Done: true}, nil
}

func (h *nativeBetaHost) Emit(context.Context, string, []byte) error {
	return nil
}

func (h *nativeBetaHost) CloseOutput(context.Context, string, string) {
	close(h.done)
}

func TestExecutorPreservesOnlyEligibleNativeResponseBeta(t *testing.T) {
	for _, test := range []struct {
		name, source, endpoint, original, payload string
		eligible                                  bool
	}{
		{"native", "responses", translate.EndpointResponses, `{"model":"test-model","input":"inspect"}`, `{}`, true},
		{"model transformation", "responses", translate.EndpointResponses, `{"model":"client-alias","input":"inspect"}`, `{}`, false},
		{"missing original", "responses", translate.EndpointResponses, ``, `{"model":"test-model","input":"inspect"}`, false},
		{"missing original model", "responses", translate.EndpointResponses, `{"input":"inspect"}`, `{}`, false},
		{"cross protocol", "claude", translate.EndpointResponses, `{"model":"test-model","messages":[{"role":"user","content":"inspect"}]}`, `{}`, false},
		{"native Messages", "claude", translate.EndpointMessages, `{"model":"test-model","messages":[{"role":"user","content":"inspect"}]}`, `{}`, false},
	} {
		for _, stream := range []bool{false, true} {
			for _, retry := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%t/retry=%t", test.name, stream, retry), func(t *testing.T) {
					host := &nativeBetaHost{inferenceHeaderHost: inferenceHeaderHost{retry: retry}, done: make(chan struct{})}
					service := reasoningService(host)
					entry := service.modelEntries["account"]
					entry.Models = []upstreamModel{{ID: "test-model", SupportedEndpoints: []string{test.endpoint}}}
					service.modelEntries["account"] = entry
					headers := http.Header{
						"Openai-Beta":           {"synthetic-beta-a", "synthetic-beta-b"},
						"Authorization":         {"Bearer synthetic-client"},
						"Cookie":                {"synthetic-cookie"},
						"Copilot-Session-Token": {"synthetic-session-token"},
						"Anthropic-Beta":        {"synthetic-anthropic-beta"},
						"X-Request-Id":          {"synthetic-request"},
					}
					before := headers.Clone()
					request := ExecuteRequest{StreamID: "output", ExecutorRequest: pluginapi.ExecutorRequest{
						AuthID: "account", Model: "test-model", SourceFormat: test.source, Headers: headers,
						OriginalRequest: []byte(test.original), Payload: []byte(test.payload),
						StorageJSON: []byte(`{"type":"copilot","github_access_token":"synthetic-credential"}`),
					}}
					if stream {
						if _, err := service.ExecuteStream(context.Background(), request); err != nil {
							t.Fatal(err)
						}
						<-host.done
					} else if _, err := service.Execute(context.Background(), request); err != nil {
						t.Fatal(err)
					}
					attempts := 1
					if retry {
						attempts = 2
					}
					if len(host.requests) != attempts || !reflect.DeepEqual(headers, before) {
						t.Fatalf("attempts = %d, inbound changed = %t", len(host.requests), !reflect.DeepEqual(headers, before))
					}
					for index, outgoing := range host.requests {
						var want []string
						if test.eligible {
							want = []string{"synthetic-beta-a", "synthetic-beta-b"}
						}
						if !reflect.DeepEqual(outgoing.Headers.Values("OpenAI-Beta"), want) {
							t.Fatalf("beta values = %#v", outgoing.Headers.Values("OpenAI-Beta"))
						}
						wantAuthorization := "Bearer synthetic-bearer"
						if index > 0 {
							wantAuthorization = "Bearer synthetic-refreshed"
						}
						if outgoing.Headers.Get("Authorization") != wantAuthorization {
							t.Fatalf("provider authorization changed on attempt %d", index)
						}
						if outgoing.Headers.Get("X-Request-Id") == "synthetic-request" {
							t.Fatal("inbound request identifier retained")
						}
						for _, name := range []string{"Cookie", "Copilot-Session-Token", "Anthropic-Beta"} {
							if len(outgoing.Headers.Values(name)) != 0 {
								t.Fatalf("forwarded header = %s", name)
							}
						}
					}
				})
			}
		}
	}
}
