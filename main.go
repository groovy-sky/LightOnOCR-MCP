package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultMaxImageBytes = 20 << 20
	maxUpstreamBodyBytes = 1 << 20
)

type config struct {
	addr            string
	mcpPath         string
	llamaURL        string
	maxImageBytes   int64
	ocrTimeout      time.Duration
	maxTokens       int
	shutdownTimeout time.Duration
}

type ocrInput struct {
	ImageURL    string `json:"image_url,omitempty" jsonschema:"HTTPS or HTTP URL of one image; mutually exclusive with image_base64"`
	ImageBase64 string `json:"image_base64,omitempty" jsonschema:"Base64-encoded image bytes; mutually exclusive with image_url"`
}

type ocrOutput struct {
	Text string `json:"text" jsonschema:"Extracted text or Markdown"`
}

type llamaRequest struct {
	Messages  []llamaMessage `json:"messages"`
	MaxTokens int            `json:"max_tokens"`
	Stream    bool           `json:"stream"`
}

type llamaMessage struct {
	Role    string         `json:"role"`
	Content []llamaContent `json:"content"`
}

type llamaContent struct {
	Type     string         `json:"type"`
	Text     string         `json:"text,omitempty"`
	ImageURL *llamaImageURL `json:"image_url,omitempty"`
}

type llamaImageURL struct {
	URL string `json:"url"`
}

type llamaResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type ocrClient struct {
	endpoint      string
	maxImageBytes int64
	maxTokens     int
	httpClient    *http.Client
	imageClient   *http.Client
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := loadConfig()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	client, err := newOCRClient(cfg)
	if err != nil {
		logger.Error("create OCR client", "error", err)
		os.Exit(1)
	}

	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:        "lightonocr-mcp",
		Title:       "LightOnOCR MCP",
		Description: "Extract text and Markdown from document images.",
		Version:     "0.1.0",
	}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{}})
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "ocr_document",
		Title:       "OCR document",
		Description: "Extract naturally ordered text or Markdown from one document image. Provide exactly one image source.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ocrInput) (*mcp.CallToolResult, ocrOutput, error) {
		text, err := client.OCR(ctx, input)
		if err != nil {
			return nil, ocrOutput{}, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, ocrOutput{Text: text}, nil
	})

	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, &mcp.StreamableHTTPOptions{
		SessionTimeout:               30 * time.Minute,
		MaxRequestBodyBytes:          encodedRequestLimit(cfg.maxImageBytes),
		PropagateRequestCancellation: true,
	})
	mux := http.NewServeMux()
	mux.Handle(cfg.mcpPath, mcpHandler)
	mux.HandleFunc("GET /healthz", client.healthHandler)

	httpServer := &http.Server{
		Addr:              cfg.addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.shutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("HTTP shutdown", "error", err)
		}
	}()

	logger.Info("server listening", "address", cfg.addr, "mcp_path", cfg.mcpPath)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("HTTP server", "error", err)
		os.Exit(1)
	}
}

func loadConfig() (config, error) {
	maxImageBytes, err := envInt64("MAX_IMAGE_BYTES", defaultMaxImageBytes)
	if err != nil || maxImageBytes <= 0 {
		return config{}, fmt.Errorf("MAX_IMAGE_BYTES must be a positive integer")
	}
	ocrTimeout, err := time.ParseDuration(env("OCR_TIMEOUT", "180s"))
	if err != nil || ocrTimeout <= 0 {
		return config{}, fmt.Errorf("OCR_TIMEOUT must be a positive duration")
	}
	mcpPath := env("MCP_PATH", "/mcp")
	if !strings.HasPrefix(mcpPath, "/") {
		return config{}, fmt.Errorf("MCP_PATH must start with /")
	}
	maxTokens, err := envInt("MAX_TOKENS", 4096)
	if err != nil || maxTokens <= 0 {
		return config{}, fmt.Errorf("MAX_TOKENS must be a positive integer")
	}
	return config{
		addr:            env("MCP_ADDR", ":8080"),
		mcpPath:         mcpPath,
		llamaURL:        strings.TrimRight(env("LLAMA_URL", "http://127.0.0.1:8000"), "/"),
		maxImageBytes:   maxImageBytes,
		ocrTimeout:      ocrTimeout,
		maxTokens:       maxTokens,
		shutdownTimeout: 10 * time.Second,
	}, nil
}

func newOCRClient(cfg config) (*ocrClient, error) {
	endpoint, err := url.Parse(cfg.llamaURL)
	if err != nil || endpoint.Scheme != "http" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, fmt.Errorf("LLAMA_URL must be a valid loopback HTTP URL")
	}
	host := endpoint.Hostname()
	ip, parseErr := netip.ParseAddr(host)
	if host != "localhost" && (parseErr != nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("LLAMA_URL must be a valid loopback HTTP URL")
	}
	httpClient := &http.Client{Timeout: cfg.ocrTimeout}
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &ocrClient{
		endpoint:      cfg.llamaURL,
		maxImageBytes: cfg.maxImageBytes,
		maxTokens:     cfg.maxTokens,
		httpClient:    httpClient,
		imageClient:   secureImageClient(cfg.ocrTimeout),
	}, nil
}

func (c *ocrClient) OCR(ctx context.Context, input ocrInput) (string, error) {
	hasURL := strings.TrimSpace(input.ImageURL) != ""
	hasBase64 := strings.TrimSpace(input.ImageBase64) != ""
	if hasURL == hasBase64 {
		return "", fmt.Errorf("provide exactly one of image_url or image_base64")
	}

	var image []byte
	var err error
	if hasURL {
		image, err = c.fetchImage(ctx, input.ImageURL)
	} else {
		image, err = decodeImage(input.ImageBase64, c.maxImageBytes)
	}
	if err != nil {
		return "", err
	}
	if err := validateImage(image); err != nil {
		return "", err
	}

	body, err := json.Marshal(llamaRequest{
		Messages: []llamaMessage{{
			Role: "user",
			Content: []llamaContent{
				{Type: "image_url", ImageURL: &llamaImageURL{URL: "data:" + http.DetectContentType(image) + ";base64," + base64.StdEncoding.EncodeToString(image)}},
			},
		}},
		MaxTokens: c.maxTokens,
		Stream:    false,
	})
	if err != nil {
		return "", fmt.Errorf("encode inference request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create inference request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("OCR inference timed out: %w", err)
		}
		return "", fmt.Errorf("OCR inference unavailable: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := readLimited(resp.Body, maxUpstreamBodyBytes)
	if err != nil {
		return "", fmt.Errorf("read inference response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OCR inference failed with status %d: %s", resp.StatusCode, cleanMessage(responseBody))
	}
	var result llamaResponse
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return "", fmt.Errorf("decode inference response: %w", err)
	}
	if len(result.Choices) == 0 || strings.TrimSpace(result.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("OCR inference returned empty text")
	}
	return result.Choices[0].Message.Content, nil
}

func (c *ocrClient) fetchImage(ctx context.Context, rawURL string) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("image_url must be a valid HTTP or HTTPS URL without credentials")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create image request: %w", err)
	}
	req.Header.Set("Accept", "image/*")
	resp, err := c.imageClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch image: unexpected status %d", resp.StatusCode)
	}
	if resp.ContentLength > c.maxImageBytes {
		return nil, fmt.Errorf("image exceeds %d-byte limit", c.maxImageBytes)
	}
	image, err := readLimited(resp.Body, c.maxImageBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch image: %w", err)
	}
	return image, nil
}

func (c *ocrClient) healthHandler(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, c.endpoint+"/health", nil)
	if err != nil {
		http.Error(w, "unhealthy", http.StatusServiceUnavailable)
		return
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		http.Error(w, "unhealthy", http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "unhealthy", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `{"status":"ok","model_ready":true}`+"\n")
}

func decodeImage(encoded string, limit int64) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	if comma := strings.IndexByte(encoded, ','); strings.HasPrefix(encoded, "data:") && comma >= 0 {
		if !strings.Contains(encoded[:comma], ";base64") {
			return nil, fmt.Errorf("image_base64 data URL must use base64 encoding")
		}
		encoded = encoded[comma+1:]
	}
	if int64(base64.StdEncoding.DecodedLen(len(encoded))) > limit+2 {
		return nil, fmt.Errorf("image exceeds %d-byte limit", limit)
	}
	image, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("image_base64 is invalid: %w", err)
	}
	if int64(len(image)) > limit {
		return nil, fmt.Errorf("image exceeds %d-byte limit", limit)
	}
	return image, nil
}

func validateImage(image []byte) error {
	if len(image) == 0 {
		return fmt.Errorf("image is empty")
	}
	mediaType := http.DetectContentType(image)
	if !strings.HasPrefix(mediaType, "image/") {
		return fmt.Errorf("input is not a recognized image")
	}
	return nil
}

func secureImageClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, address := range addresses {
			if !publicAddress(address) {
				return nil, fmt.Errorf("image_url resolves to a non-public address")
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].String(), port))
	}
	client := &http.Client{Transport: transport, Timeout: timeout}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("redirect uses unsupported scheme")
		}
		return nil
	}
	return client
}

func publicAddress(address netip.Addr) bool {
	return address.IsValid() && !address.IsPrivate() && !address.IsLoopback() && !address.IsLinkLocalUnicast() &&
		!address.IsLinkLocalMulticast() && !address.IsMulticast() && !address.IsUnspecified()
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("body exceeds %d-byte limit", limit)
	}
	return data, nil
}

func cleanMessage(message []byte) string {
	cleaned := strings.Join(strings.Fields(string(message)), " ")
	if len(cleaned) > 256 {
		cleaned = cleaned[:256]
	}
	if cleaned == "" {
		return "no details"
	}
	return cleaned
}

func encodedRequestLimit(imageBytes int64) int64 {
	return imageBytes*4/3 + (1 << 20)
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envInt64(name string, fallback int64) (int64, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	return strconv.ParseInt(value, 10, 64)
}

func envInt(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	return strconv.Atoi(value)
}
