package main

import (
	"fmt"

	"github.com/maksymenkoml/lingoose/tool/shell"
)

func main() {
	t := shell.New()

	bashScript := `echo "Hello from $SHELL!"`
	f := t.Fn().(shell.FnPrototype)

	fmt.Println(f(shell.Input{BashScript: bashScript}))
}
