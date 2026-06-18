// Command bot is the SpaceCraft Discord bot entrypoint.
package main

import (
	"fmt"
	"os"

	"github.com/kweezl/spacecraft-cadet/internal/app"
	"go.uber.org/fx"
)

func main() {
	opts, err := app.Options()
	if err != nil {
		fmt.Fprintln(os.Stderr, "startup:", err)
		os.Exit(1)
	}
	fx.New(opts...).Run()
}
