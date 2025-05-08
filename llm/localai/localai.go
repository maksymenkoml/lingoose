package localai

import (
	"os"

	goopenai "github.com/sashabaranov/go-openai"

	"github.com/maksymenkoml/lingoose/llm/openai"
)

type LocalAI struct {
	*openai.OpenAI
}

func New(endpoint string) *LocalAI {
	customConfig := goopenai.DefaultConfig(os.Getenv("OPENAI_API_KEY"))
	customConfig.BaseURL = endpoint
	customClient := goopenai.NewClientWithConfig(customConfig)

	openaillm := openai.New().WithClient(customClient)
	openaillm.Name = "localai"
	return &LocalAI{
		OpenAI: openaillm,
	}
}
