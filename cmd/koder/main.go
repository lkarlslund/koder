package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	os.Exit(runMain())
}

func runMain() (code int) {
	stopProfiling, err := startRuntimeProfilingFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() {
		if stopProfiling == nil {
			return
		}
		if err := stopProfiling(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			if code == 0 {
				code = 1
			}
		}
	}()

	if err := NewRootCommand().ExecuteContext(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
