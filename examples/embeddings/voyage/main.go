package main

import (
	"context"
	"fmt"

	voyageembedder "github.com/maksymenkoml/lingoose/embedder/voyage"
	"github.com/maksymenkoml/lingoose/index"
	indexoption "github.com/maksymenkoml/lingoose/index/option"
	"github.com/maksymenkoml/lingoose/index/vectordb/jsondb"
	"github.com/maksymenkoml/lingoose/llm/anthropic"
	"github.com/maksymenkoml/lingoose/loader"
	"github.com/maksymenkoml/lingoose/textsplitter"
	"github.com/maksymenkoml/lingoose/thread"
	"github.com/maksymenkoml/lingoose/types"
)

// download https://raw.githubusercontent.com/hwchase17/chat-your-data/master/state_of_the_union.txt

func main() {

	index := index.New(
		jsondb.New().WithPersist("db.json"),
		voyageembedder.New().WithModel("voyage-2"),
	).WithIncludeContents(true).WithAddDataCallback(func(data *index.Data) error {
		data.Metadata["contentLen"] = len(data.Metadata["content"].(string))
		return nil
	})

	indexIsEmpty, _ := index.IsEmpty(context.Background())

	if indexIsEmpty {
		err := ingestData(index)
		if err != nil {
			panic(err)
		}
	}

	query := "What is the purpose of the NATO Alliance?"
	similarities, err := index.Query(
		context.Background(),
		query,
		indexoption.WithTopK(3),
	)
	if err != nil {
		panic(err)
	}

	for _, similarity := range similarities {
		fmt.Printf("Similarity: %f\n", similarity.Score)
		fmt.Printf("Document: %s\n", similarity.Content())
		fmt.Println("Metadata: ", similarity.Metadata)
		fmt.Println("----------")
	}

	documentContext := ""
	for _, similarity := range similarities {
		documentContext += similarity.Content() + "\n\n"
	}

	anthropicllm := anthropic.New().WithModel("claude-3-opus-20240229")
	t := thread.New()
	t.AddMessage(thread.NewUserMessage().AddContent(
		thread.NewTextContent("Based on the following context answer to the" +
			"question.\n\nContext:\n{{.context}}\n\nQuestion: {{.query}}").Format(
			types.M{
				"query":   query,
				"context": documentContext,
			},
		),
	))

	err = anthropicllm.Generate(context.Background(), t)
	if err != nil {
		panic(err)
	}

	fmt.Println(t)
}

func ingestData(index *index.Index) error {

	fmt.Printf("Ingesting data...")

	documents, err := loader.NewDirectoryLoader(".", ".txt").Load(context.Background())
	if err != nil {
		return err
	}

	textSplitter := textsplitter.NewRecursiveCharacterTextSplitter(1000, 20)

	documentChunks := textSplitter.SplitDocuments(documents)

	err = index.LoadFromDocuments(context.Background(), documentChunks)
	if err != nil {
		return err
	}

	fmt.Printf("Done!\n")

	return nil
}
