package main

import (
	"context"
	"fmt"

	"github.com/maksymenkoml/lingoose/loader"
)

func main() {

	l := loader.NewCSVLoader("/tmp/cities.csv").WithLazyQuotes()

	docs, err := l.Load(context.Background())
	if err != nil {
		panic(err)
	}

	fmt.Println(docs[0].Content)

}
