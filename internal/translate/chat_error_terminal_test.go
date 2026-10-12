package translate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestChatErrorFinishReasonFailsCrossFormatResponses(t *testing.T) {
	t.Parallel()
	request, response := geminiChatSearchCapture(t)
	translated, err := RequestForEndpointFrom("openai", "gemini-3.8-flash", request, false, EndpointResponses)
	if err != nil {
		t.Fatal(err)
	}

	got, err := ResponseFromEndpoint(context.Background(), EndpointChatCompletions, "openai-response", "gemini-3.8-flash", request, translated, response)
	if err == nil {
		t.Fatalf("Chat finish_reason=error became a successful Responses body: %s", got)
	}
}

func TestChatErrorFinishReasonFailsCrossFormatMessages(t *testing.T) {
	t.Parallel()
	request, response := geminiChatSearchCapture(t)
	translated, err := RequestForEndpointFrom("openai", "gemini-3.8-flash", request, false, EndpointMessages)
	if err != nil {
		t.Fatal(err)
	}

	got, err := ResponseFromEndpoint(context.Background(), EndpointChatCompletions, "claude", "gemini-3.8-flash", request, translated, response)
	if err == nil {
		t.Fatalf("Chat finish_reason=error became a successful Messages body: %s", got)
	}
}

func TestChatErrorFinishReasonStopsCrossFormatStreams(t *testing.T) {
	t.Parallel()
	request, _ := geminiChatSearchCapture(t)
	for _, test := range []struct {
		name        string
		destination string
		endpoint    string
	}{
		{name: "Responses", destination: "openai-response", endpoint: EndpointResponses},
		{name: "Messages", destination: "claude", endpoint: EndpointMessages},
	} {
		t.Run(test.name, func(t *testing.T) {
			translated, err := RequestForEndpointFrom("openai", "gemini-3.8-flash", request, true, test.endpoint)
			if err != nil {
				t.Fatal(err)
			}
			frames := [][]byte{
				[]byte("data: {\"id\":\"fixture-error-stream\",\"object\":\"chat.completion.chunk\",\"model\":\"gemini-3.8-flash\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n"),
				[]byte("data: {\"id\":\"fixture-error-stream\",\"object\":\"chat.completion.chunk\",\"model\":\"gemini-3.8-flash\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"error\"}]}\n\n"),
				[]byte("data: {\"id\":\"fixture-error-stream\",\"object\":\"chat.completion.chunk\",\"model\":\"gemini-3.8-flash\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"late success\"},\"finish_reason\":\"stop\"}]}\n\n"),
				[]byte("data: [DONE]\n\n"),
			}
			var state any
			var output [][]byte
			translatedFrames, err := StreamFromEndpoint(context.Background(), EndpointChatCompletions, test.destination, "gemini-3.8-flash", request, translated, frames[0], &state)
			if err != nil {
				t.Fatalf("translate partial output: %v", err)
			}
			output = append(output, translatedFrames...)
			if _, err := StreamFromEndpoint(context.Background(), EndpointChatCompletions, test.destination, "gemini-3.8-flash", request, translated, frames[1], &state); err == nil {
				t.Fatal("Chat finish_reason=error did not fail the translated stream")
			}
			if _, err := StreamFromEndpoint(context.Background(), EndpointChatCompletions, test.destination, "gemini-3.8-flash", request, translated, frames[2], &state); err == nil {
				t.Fatal("stream accepted a later success frame after Chat failure")
			}
			if _, err := StreamFromEndpoint(context.Background(), EndpointChatCompletions, test.destination, "gemini-3.8-flash", request, translated, frames[3], &state); err == nil {
				t.Fatal("stream accepted [DONE] after Chat failure")
			}
			for _, frame := range output {
				if strings.Contains(string(frame), "response.completed") || strings.Contains(string(frame), `"stop_reason":"end_turn"`) || strings.Contains(string(frame), "late success") {
					t.Fatalf("translated stream emitted success after Chat error: %s", frame)
				}
			}
		})
	}
}

func TestNativeChatErrorFinishReasonRemainsPassthrough(t *testing.T) {
	t.Parallel()
	_, response := geminiChatSearchCapture(t)
	got, err := ResponseFromEndpoint(context.Background(), EndpointChatCompletions, "openai", "gemini-3.8-flash", nil, nil, response)
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(got, "choices.0.finish_reason").String() != "error" {
		t.Fatalf("native Chat response finish reason changed: %s", got)
	}

	frame := []byte("data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"error\"}]}\n\n")
	var state any
	chunks, err := StreamFromEndpoint(context.Background(), EndpointChatCompletions, "openai", "gemini-3.8-flash", nil, nil, frame, &state)
	if err != nil || len(chunks) != 1 || string(chunks[0]) != string(frame) {
		t.Fatalf("native Chat error frame changed: chunks=%q err=%v", chunks, err)
	}
}

func geminiChatSearchCapture(t *testing.T) ([]byte, []byte) {
	t.Helper()
	root := filepath.Join("testdata", "native-server-tool-captures", "gemini-chat-search")
	request, err := os.ReadFile(filepath.Join(root, "request.json"))
	if err != nil {
		t.Fatal(err)
	}
	response, err := os.ReadFile(filepath.Join(root, "response.json"))
	if err != nil {
		t.Fatal(err)
	}
	return request, response
}
