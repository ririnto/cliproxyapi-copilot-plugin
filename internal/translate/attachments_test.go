package translate

import (
	"errors"
	"testing"

	"github.com/tidwall/gjson"
)

func TestClaudeToolResultDocumentProjection(t *testing.T) {
	body := []byte(`{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_a","name":"read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_a","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"JVBERi0xLjQK"}}]}]}]}`)
	out, err := ClaudeToResponses("test-model", body, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := gjson.GetBytes(out, `input.#(type=="function_call_output").output.0.file_data`).String(); got != "data:application/pdf;base64,JVBERi0xLjQK" {
		t.Fatalf("tool result file_data = %q", got)
	}
	back, err := RequestForEndpointFrom("openai-response", "test-model", out, false, EndpointMessages)
	if err != nil {
		t.Fatal(err)
	}
	if got := gjson.GetBytes(back, `messages.#(role=="user").content.#(type=="tool_result").content.0.source.data`).String(); got != "JVBERi0xLjQK" {
		t.Fatalf("tool result document bytes = %q", got)
	}
}

func TestResponsesFileURLCannotDisappear(t *testing.T) {
	body := []byte(`{"input":[{"role":"user","content":[{"type":"input_file","file_url":"https://media.example.invalid/report.pdf"}]}]}`)
	out, err := RequestForEndpointFrom("openai-response", "test-model", body, false, EndpointMessages)
	if out != nil || !errors.Is(err, ErrAttachmentConversion) {
		t.Fatalf("payload present = %v, error present = %v", out != nil, err != nil)
	}
}

func TestAttachmentConversionsPreserveStructuredFields(t *testing.T) {
	for _, test := range []struct {
		name     string
		source   string
		endpoint string
		body     string
		fields   map[string]string
	}{
		{"document bytes and title", "claude", EndpointResponses, `{"messages":[{"role":"user","content":[{"type":"document","title":" report.pdf ","source":{"type":"base64","media_type":"application/pdf","data":"JVBERi0xLjQK"}}]}]}`, map[string]string{"input.0.content.0.type": "input_file", "input.0.content.0.file_data": "data:application/pdf;base64,JVBERi0xLjQK", "input.0.content.0.filename": " report.pdf "}},
		{"document URL", "claude", EndpointResponses, `{"messages":[{"role":"user","content":[{"type":"document","source":{"type":"url","url":"https://media.example.invalid/report.pdf"}}]}]}`, map[string]string{"input.0.content.0.file_url": "https://media.example.invalid/report.pdf"}},
		{"image URL", "claude", EndpointChatCompletions, `{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"https://media.example.invalid/image.png"}}]}]}`, map[string]string{"messages.0.content.0.type": "image_url", "messages.0.content.0.image_url.url": "https://media.example.invalid/image.png"}},
		{"image inline", "claude", EndpointResponses, `{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]}]}`, map[string]string{"input.0.content.0.image_url": "data:image/png;base64,AA=="}},
		{"PDF to Messages", "openai-response", EndpointMessages, `{"input":[{"role":"user","content":[{"type":"input_file","file_data":"data:application/pdf;base64,JVBERi0xLjQK"}]}]}`, map[string]string{"messages.0.content.0.type": "document", "messages.0.content.0.source.media_type": "application/pdf", "messages.0.content.0.source.data": "JVBERi0xLjQK"}},
		{"image detail to Chat", "openai-response", EndpointChatCompletions, `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"https://media.example.invalid/image.png","detail":"high"}]}]}`, map[string]string{"messages.0.content.0.image_url.url": "https://media.example.invalid/image.png", "messages.0.content.0.image_url.detail": "high"}},
		{"image bytes to Messages", "openai-response", EndpointMessages, `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}]}`, map[string]string{"messages.0.content.0.source.media_type": "image/png", "messages.0.content.0.source.data": "AA=="}},
		{"tool result image to Chat", "openai-response", EndpointChatCompletions, `{"input":[{"type":"function_call_output","call_id":"call_a","output":[{"type":"input_image","image_url":"https://media.example.invalid/image.png","detail":"low"}]}]}`, map[string]string{"messages.0.role": "tool", "messages.0.tool_call_id": "call_a", "messages.0.content.0.image_url.detail": "low"}},
		{"native document owner", "claude", EndpointMessages, `{"messages":[{"role":"user","content":[{"type":"document","context":"synthetic context","source":{"type":"file","file_id":"synthetic-file"}}]}]}`, map[string]string{"messages.0.content.0.source.file_id": "synthetic-file", "messages.0.content.0.context": "synthetic context"}},
		{"native file owner", "openai-response", EndpointResponses, `{"input":[{"role":"user","content":[{"type":"input_file","file_id":"synthetic-file","filename":"report.pdf"}]}]}`, map[string]string{"input.0.content.0.file_id": "synthetic-file", "input.0.content.0.filename": "report.pdf"}},
		{"native image detail", "openai-response", EndpointResponses, `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"https://media.example.invalid/image.png","detail":"original"}]}]}`, map[string]string{"input.0.content.0.detail": "original"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			out, err := RequestForEndpointFrom(test.source, "test-model", []byte(test.body), true, test.endpoint)
			if err != nil {
				t.Fatal(err)
			}
			for path, want := range test.fields {
				if got := gjson.GetBytes(out, path).String(); got != want {
					t.Fatalf("%s = %q, want %q", path, got, want)
				}
			}
			if !gjson.GetBytes(out, "stream").Bool() || gjson.GetBytes(out, "model").String() != "test-model" {
				t.Fatalf("dispatch fields = %s", out)
			}
		})
	}
}

func TestLossyAttachmentsReturnTypedConversionErrors(t *testing.T) {
	for _, test := range []struct {
		name     string
		source   string
		endpoint string
		body     string
		kind     string
		field    string
	}{
		{"Messages PDF to Chat", "claude", EndpointChatCompletions, `{"messages":[{"role":"user","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"JVBERi0xLjQK"}}]}]}`, "document", "type"},
		{"Messages document owner", "claude", EndpointResponses, `{"messages":[{"role":"user","content":[{"type":"document","source":{"type":"file","file_id":"synthetic-file"}}]}]}`, "document", "source.type"},
		{"Messages document context", "claude", EndpointResponses, `{"messages":[{"role":"user","content":[{"type":"document","context":"synthetic context","source":{"type":"url","url":"https://media.example.invalid/report.pdf"}}]}]}`, "document", "context"},
		{"Messages citations", "claude", EndpointResponses, `{"messages":[{"role":"user","content":[{"type":"document","citations":{"enabled":true},"source":{"type":"url","url":"https://media.example.invalid/report.pdf"}}]}]}`, "document", "citations"},
		{"Messages image owner", "claude", EndpointResponses, `{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"file","file_id":"synthetic-file"}}]}]}`, "image", "source.type"},
		{"Responses document URL", "openai-response", EndpointMessages, `{"input":[{"role":"user","content":[{"type":"input_file","file_url":"https://media.example.invalid/report.pdf"}]}]}`, "input_file", "file_url"},
		{"Responses document owner", "openai-response", EndpointMessages, `{"input":[{"role":"user","content":[{"type":"input_file","file_id":"synthetic-file"}]}]}`, "input_file", "file_id"},
		{"Responses filename", "openai-response", EndpointMessages, `{"input":[{"role":"user","content":[{"type":"input_file","filename":"report.pdf","file_data":"data:application/pdf;base64,JVBERi0xLjQK"}]}]}`, "input_file", "filename"},
		{"Responses PDF to Chat", "openai-response", EndpointChatCompletions, `{"input":[{"role":"user","content":[{"type":"input_file","file_data":"data:application/pdf;base64,JVBERi0xLjQK"}]}]}`, "input_file", "type"},
		{"Responses original detail to Chat", "openai-response", EndpointChatCompletions, `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"https://media.example.invalid/image.png","detail":"original"}]}]}`, "input_image", "detail"},
		{"Responses detail to Messages", "openai-response", EndpointMessages, `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"https://media.example.invalid/image.png","detail":"high"}]}]}`, "input_image", "detail"},
		{"Responses image owner", "openai-response", EndpointMessages, `{"input":[{"role":"user","content":[{"type":"input_image","file_id":"synthetic-file"}]}]}`, "input_image", "file_id"},
		{"Responses audio", "openai-response", EndpointChatCompletions, `{"input":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"AA==","format":"wav"}}]}]}`, "input_audio", "type"},
		{"Messages audio", "claude", EndpointResponses, `{"messages":[{"role":"user","content":[{"type":"audio","data":"AA=="}]}]}`, "audio", "type"},
		{"tool output document URL", "openai-response", EndpointMessages, `{"input":[{"type":"function_call_output","call_id":"call_a","output":[{"type":"input_file","file_url":"https://media.example.invalid/report.pdf"}]}]}`, "input_file", "file_url"},
		{"tool output document to Chat", "claude", EndpointChatCompletions, `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_a","content":[{"type":"document","source":{"type":"url","url":"https://media.example.invalid/report.pdf"}}]}]}]}`, "document", "type"},
		{"custom image to Chat", "openai-response", EndpointChatCompletions, `{"input":[{"type":"custom_tool_call_output","call_id":"call_a","output":[{"type":"input_image","image_url":"https://media.example.invalid/image.png"}]}]}`, "input_image", "output"},
		{"custom image to Messages", "openai-response", EndpointMessages, `{"input":[{"type":"custom_tool_call_output","call_id":"call_a","output":[{"type":"input_image","image_url":"https://media.example.invalid/image.png"}]}]}`, "input_image", "output"},
		{"custom PDF to Messages", "openai-response", EndpointMessages, `{"input":[{"type":"custom_tool_call_output","call_id":"call_a","output":[{"type":"input_file","file_data":"data:application/pdf;base64,JVBERi0xLjQK"}]}]}`, "input_file", "output"},
		{"malformed document URL", "claude", EndpointResponses, `{"messages":[{"role":"user","content":[{"type":"document","source":{"type":"url","url":17}}]}]}`, "document", "source.url"},
		{"missing image data", "claude", EndpointChatCompletions, `{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png"}}]}]}`, "image", "source.data"},
		{"malformed document MIME", "claude", EndpointResponses, `{"messages":[{"role":"user","content":[{"type":"document","source":{"type":"base64","media_type":17,"data":"AA=="}}]}]}`, "document", "source.media_type"},
		{"malformed document title", "claude", EndpointResponses, `{"messages":[{"role":"user","content":[{"type":"document","title":17,"source":{"type":"text","data":"synthetic"}}]}]}`, "document", "title"},
		{"malformed Responses image", "openai-response", EndpointChatCompletions, `{"input":[{"role":"user","content":[{"type":"input_image","image_url":17}]}]}`, "input_image", "image_url"},
		{"unresolved MIME", "openai-response", EndpointMessages, `{"input":[{"role":"user","content":[{"type":"input_file","file_data":"JVBERi0xLjQK"}]}]}`, "input_file", "file_data"},
	} {
		t.Run(test.name, func(t *testing.T) {
			out, err := RequestForEndpointFrom(test.source, "test-model", []byte(test.body), false, test.endpoint)
			var detail *AttachmentConversionError
			if out != nil || !errors.Is(err, ErrAttachmentConversion) || !errors.As(err, &detail) {
				t.Fatalf("payload present = %v, error type = %T", out != nil, err)
			}
			destination := map[string]string{"/chat/completions": "openai", "/responses": "openai-response", "/v1/messages": "claude"}[test.endpoint]
			if detail.Source != test.source || detail.Destination != destination || detail.Kind != test.kind || detail.Field != test.field {
				t.Fatalf("conversion fields = %#v", detail)
			}
		})
	}
}

func TestClaudeToResponsesRejectsAttachmentMetadataLoss(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"document","context":"synthetic context","source":{"type":"url","url":"https://media.example.invalid/report.pdf"}}]}]}`)
	out, err := ClaudeToResponses("test-model", body, false)
	var detail *AttachmentConversionError
	if out != nil || !errors.Is(err, ErrAttachmentConversion) || !errors.As(err, &detail) || detail.Field != "context" || detail.Kind != "document" {
		t.Fatalf("payload present = %v, conversion error = %#v", out != nil, err)
	}
}
