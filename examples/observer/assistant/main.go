package main

import (
	"context"
	"fmt"
	"os"

	"github.com/maksymenkoml/lingoose/assistant"
	openaiembedder "github.com/maksymenkoml/lingoose/embedder/openai"
	"github.com/maksymenkoml/lingoose/index"
	"github.com/maksymenkoml/lingoose/index/vectordb/jsondb"
	"github.com/maksymenkoml/lingoose/llm/openai"
	"github.com/maksymenkoml/lingoose/observer"
	"github.com/maksymenkoml/lingoose/observer/langfuse"
	"github.com/maksymenkoml/lingoose/rag"
	"github.com/maksymenkoml/lingoose/thread"
)

// download https://raw.githubusercontent.com/hwchase17/chat-your-data/master/state_of_the_union.txt

func main() {
	ctx := context.Background()

	o := langfuse.New(ctx)
	trace, err := o.Trace(&observer.Trace{Name: "state of the union"})
	if err != nil {
		panic(err)
	}

	ctx = observer.ContextWithObserverInstance(ctx, o)
	ctx = observer.ContextWithTraceID(ctx, trace.ID)

	r := rag.New(
		index.New(
			jsondb.New().WithPersist("db.json"),
			openaiembedder.New(openaiembedder.AdaEmbeddingV2),
		),
	).WithTopK(3)

	_, err = os.Stat("db.json")
	if os.IsNotExist(err) {
		err = r.AddSources(ctx, "state_of_the_union.txt")
		if err != nil {
			panic(err)
		}
	}

	a := assistant.New(
		openai.New().WithTemperature(0),
	).WithParameters(
		assistant.Parameters{
			AssistantName:      "AI Pirate Assistant",
			AssistantIdentity:  "a pirate and helpful assistant",
			AssistantScope:     "with their questions replying as a pirate",
			CompanyName:        "Lingoose",
			CompanyDescription: "a pirate company that provides AI assistants to help humans with their questions",
		},
	).WithRAG(r).WithThread(
		thread.New().AddMessages(
			thread.NewUserMessage().AddContent(
				thread.NewTextContent("what is the purpose of NATO?"),
			),
		),
	)

	err = a.Run(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println("----")
	fmt.Println(a.Thread())
	fmt.Println("----")

	o.Flush(ctx)
}
