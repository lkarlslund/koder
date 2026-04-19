package main

import (
	"context"
	"fmt"
	"os"

	"github.com/lkarlslund/koder/internal/app"
)

func main() {
	if err := app.NewRootCommand().ExecuteContext(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
