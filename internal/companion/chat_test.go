package companion

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func chatClient(body string) *http.Client {
	return &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
}

func TestGenerateAnswerRejectsInventedCitation(t *testing.T) {
	pack := ContextPack{}
	pack.Evidence = []Citation{{ID: "e1", Path: "a.py", StartLine: 1, EndLine: 1, Snippet: "x"}}
	_, err := GenerateAnswer(context.Background(), ChatConfig{Provider: "openai_compatible", BaseURL: "https://test.invalid/chat", Model: "test", APIKey: "not-a-secret", HTTPClient: chatClient(`{"choices":[{"message":{"content":"{\"answer\":\"claim [e99]\",\"citations\":[\"e99\"]}"}}]}`)}, pack)
	if err == nil {
		t.Fatal("invented citation accepted")
	}
}

func TestGenerateAnswerAcceptsEvidenceCitation(t *testing.T) {
	pack := ContextPack{}
	pack.Evidence = []Citation{{ID: "e1", Path: "a.py", StartLine: 1, EndLine: 1, Snippet: "x"}}
	got, err := GenerateAnswer(context.Background(), ChatConfig{Provider: "openai_compatible", BaseURL: "https://test.invalid/chat", Model: "test", APIKey: "not-a-secret", HTTPClient: chatClient(`{"choices":[{"message":{"content":"{\"answer\":\"grounded [e1]\",\"citations\":[\"e1\"]}"}}]}`)}, pack)
	if err != nil || got.Text != "grounded [e1]" {
		t.Fatalf("GenerateAnswer=%+v, %v", got, err)
	}
}
