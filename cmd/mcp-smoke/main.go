package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: mcp-smoke <endpoint> <image-file> <image-url>")
		os.Exit(2)
	}

	image, err := os.ReadFile(os.Args[2])
	if err != nil {
		fail("read image: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "release-smoke", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: os.Args[1]}, nil)
	if err != nil {
		fail("connect: %v", err)
	}
	defer session.Close()

	inputs := []map[string]any{
		{"image_base64": base64.StdEncoding.EncodeToString(image)},
		{"image_url": os.Args[3]},
	}
	for index, input := range inputs {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "ocr_document", Arguments: input})
		if err != nil {
			fail("tool call %d: %v", index+1, err)
		}
		if result.IsError || !hasText(result.Content) {
			fail("tool call %d returned an error or empty text", index+1)
		}
	}
}

func hasText(contents []mcp.Content) bool {
	for _, content := range contents {
		if text, ok := content.(*mcp.TextContent); ok && text.Text != "" {
			return true
		}
	}
	return false
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
