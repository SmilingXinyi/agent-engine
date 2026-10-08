package main

import (
	"context"
	"fmt"
	"log"

	aisdk "github.com/SmilingXinyi/agent-engine/ai-sdk"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/model"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/provider"
)

func main() {
	ctx := context.Background()

	// Initialize mock provider for demo
	mockProvider := provider.NewMockProvider()
	mockProvider.RegisterResponse("demo-gpt", &model.GenerateResponse{
		Text: "Hello from ai-sdk in Go workspace!",
	})

	m, err := mockProvider.Model("demo-gpt")
	if err != nil {
		log.Fatalf("failed to get model: %v", err)
	}

	fmt.Println("--- 1. GenerateText ---")
	text, err := aisdk.GenerateText(ctx, m, "Hello world")
	if err != nil {
		log.Fatalf("generate failed: %v", err)
	}
	fmt.Printf("Generated: %s\n", text)

	fmt.Println("\n--- 2. StreamText ---")
	_, err = aisdk.StreamText(ctx, m, "Hello stream", func(chunk string) error {
		fmt.Print(chunk)
		return nil
	})
	if err != nil {
		log.Fatalf("stream failed: %v", err)
	}
	fmt.Println()
}
