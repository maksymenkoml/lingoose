package main

import (
	"context"
	"fmt"

	"github.com/maksymenkoml/lingoose/loader"
)

func main() {

	l := loader.NewHFSpeechRecognitionLoader("/tmp/hello.mp3")

	docs, err := l.Load(context.Background())
	if err != nil {
		panic(err)
	}

	fmt.Println(docs[0].Content)

}
