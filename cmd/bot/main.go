// Command bot is the SpaceCraft Discord bot entrypoint.
package main

import (
	"fmt"
	"os"

	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/app"
)

func main() {
	opts, err := app.Options()
	if err != nil {
		fmt.Fprintln(os.Stderr, "startup:", err)
		os.Exit(1)
	}
	fx.New(opts...).Run()
}
