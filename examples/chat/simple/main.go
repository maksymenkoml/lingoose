package main

import (
	"context"
	"fmt"

	"github.com/maksymenkoml/lingoose/legacy/chat"
	"github.com/maksymenkoml/lingoose/legacy/prompt"
	"github.com/maksymenkoml/lingoose/llm/openai"
)

func main() {

	chat := chat.New(
		chat.PromptMessage{
			Type:   chat.MessageTypeSystem,
			Prompt: prompt.New("You are a professional joke writer"),
		},
		chat.PromptMessage{
			Type:   chat.MessageTypeUser,
			Prompt: prompt.New("Write a joke about a goose"),
		},
	)

	llmOpenAI := openai.NewLegacy(openai.GPT3Dot5Turbo, openai.DefaultOpenAITemperature, openai.DefaultOpenAIMaxTokens, true)

	response, err := llmOpenAI.Chat(context.Background(), chat)
	if err != nil {
		panic(err)
	}

	fmt.Printf("\n%#v", response)

}
