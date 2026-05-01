package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	if err := NewRootCommand().ExecuteContext(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
