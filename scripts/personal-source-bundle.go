//go:build ignore

// Write the SDK source bundle from the same module graph used by the app.
package main

import (
	"fmt"
	"os"

	"github.com/airlockrun/agentsdk/sourcebundle"
)

func main() {
	if len(os.Args) != 3 {
		panic("usage: bundle <app directory> <archive>")
	}
	out, err := os.OpenFile(os.Args[2], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		panic(err)
	}
	state, err := sourcebundle.WriteArchive(out, os.Args[1])
	if err != nil {
		panic(err)
	}
	if err = out.Close(); err != nil {
		panic(err)
	}
	fmt.Println(state)
}
