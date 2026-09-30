package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var tinyPNG = []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 504))

func TestOCRBase64Success(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		var request llamaRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.MaxTokens != 4096 || request.Stream || len(request.Messages) != 1 || len(request.Messages[0].Content) != 1 {
			t.Fatalf("unexpected request: %#v", request)
		}
		dataURL := request.Messages[0].Content[0].ImageURL.URL
		encoded := strings.TrimPrefix(dataURL, "data:image/png;base64,")
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || string(decoded) != string(tinyPNG) {
			t.Fatalf("forwarded image mismatch: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": "# Receipt\nTotal: 12.00"}}},
		})
	}))
	defer upstream.Close()

	client := testClient(upstream.URL, int64(len(tinyPNG)))
	text, err := client.OCR(context.Background(), ocrInput{ImageBase64: base64.StdEncoding.EncodeToString(tinyPNG)})
	if err != nil {
		t.Fatal(err)
	}
	if text != "# Receipt\nTotal: 12.00" {
		t.Fatalf("text = %q", text)
	}
}

func TestOCRURLSuccess(t *testing.T) {
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	}))
	defer imageServer.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": "downloaded image"}}},
		})
	}))
	defer upstream.Close()
	client := testClient(upstream.URL, int64(len(tinyPNG)))

	text, err := client.OCR(context.Background(), ocrInput{ImageURL: imageServer.URL + "/document.png"})
	if err != nil {
		t.Fatal(err)
	}
	if text != "downloaded image" {
		t.Fatalf("text = %q", text)
	}
}

func TestOCRValidation(t *testing.T) {
	client := testClient("http://127.0.0.1:1", int64(len(tinyPNG)))
	tests := []struct {
		name  string
		input ocrInput
		want  string
	}{
		{name: "neither", input: ocrInput{}, want: "exactly one"},
		{name: "both", input: ocrInput{ImageURL: "https://example.com/a.png", ImageBase64: "eA=="}, want: "exactly one"},
		{name: "invalid base64", input: ocrInput{ImageBase64: "%%%"}, want: "invalid"},
		{name: "not image", input: ocrInput{ImageBase64: base64.StdEncoding.EncodeToString([]byte("hello"))}, want: "recognized image"},
		{name: "too large", input: ocrInput{ImageBase64: base64.StdEncoding.EncodeToString(append(tinyPNG, 'x'))}, want: "exceeds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := client.OCR(context.Background(), test.input)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestOCRUpstreamFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model overloaded\ninternal detail", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()
	client := testClient(upstream.URL, int64(len(tinyPNG)))

	_, err := client.OCR(context.Background(), ocrInput{ImageBase64: base64.StdEncoding.EncodeToString(tinyPNG)})
	if err == nil || !strings.Contains(err.Error(), "status 503: model overloaded internal detail") {
		t.Fatalf("error = %v", err)
	}
}

func TestOCRUpstreamResponseValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "empty choices", body: `{"choices":[]}`, want: "empty text"},
		{name: "invalid JSON", body: `{`, want: "decode inference response"},
		{name: "too large", body: strings.Repeat("x", maxUpstreamBodyBytes+1), want: "body exceeds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.body))
			}))
			defer upstream.Close()
			client := testClient(upstream.URL, int64(len(tinyPNG)))

			_, err := client.OCR(context.Background(), ocrInput{ImageBase64: base64.StdEncoding.EncodeToString(tinyPNG)})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestOCRTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer upstream.Close()
	client := testClient(upstream.URL, int64(len(tinyPNG)))
	client.httpClient.Timeout = 10 * time.Millisecond

	_, err := client.OCR(context.Background(), ocrInput{ImageBase64: base64.StdEncoding.EncodeToString(tinyPNG)})
	if err == nil || !strings.Contains(err.Error(), "timed out") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func TestOCRCancellation(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	client := testClient("http://llama.invalid", int64(len(tinyPNG)))
	client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		close(cancelled)
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())

	errChannel := make(chan error, 1)
	go func() {
		_, err := client.OCR(ctx, ocrInput{ImageBase64: base64.StdEncoding.EncodeToString(tinyPNG)})
		errChannel <- err
	}()
	<-started
	cancel()
	err := <-errChannel
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("upstream request was not cancelled")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestHealthReflectsInferenceReadiness(t *testing.T) {
	status := http.StatusServiceUnavailable
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.WriteHeader(status)
	}))
	defer upstream.Close()
	client := testClient(upstream.URL, int64(len(tinyPNG)))

	recorder := httptest.NewRecorder()
	client.healthHandler(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unready status = %d", recorder.Code)
	}

	status = http.StatusOK
	recorder = httptest.NewRecorder()
	client.healthHandler(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"model_ready":true`) {
		t.Fatalf("ready response = %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestNewOCRClientRequiresLoopback(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "IPv4 loopback", url: "http://127.0.0.1:8000"},
		{name: "IPv6 loopback", url: "http://[::1]:8000"},
		{name: "localhost", url: "http://localhost:8000"},
		{name: "TLS", url: "https://localhost:8000", wantErr: true},
		{name: "remote", url: "http://example.com:8000", wantErr: true},
		{name: "credentials", url: "http://user@localhost:8000", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := newOCRClient(config{llamaURL: test.url, maxImageBytes: 1, maxTokens: 1, ocrTimeout: time.Second})
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, test.wantErr)
			}
		})
	}
}

func testClient(endpoint string, maxImageBytes int64) *ocrClient {
	return &ocrClient{
		endpoint:      endpoint,
		maxImageBytes: maxImageBytes,
		maxTokens:     4096,
		httpClient:    &http.Client{Timeout: time.Second},
		imageClient:   &http.Client{Timeout: time.Second},
	}
}
