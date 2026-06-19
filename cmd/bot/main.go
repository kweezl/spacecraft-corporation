// Command bot is the SpaceCraft Discord bot entrypoint.
package main

import (
	"flag"
	"fmt"
	"os"
	// Embed the IANA timezone database so APP_TIMEZONE resolves any named zone
	// without relying on OS tzdata (absent in the minimal prod image).
	_ "time/tzdata"

	"go.uber.org/fx"

	"github.com/kweezl/spacecraft-corporation/internal/app"
)

func main() {
	// --migrate runs the embedded migrations once and exits. Without it the bot
	// starts normally and never touches the schema (migrations are a separate,
	// explicit step — the `migrate` compose service runs this before the bot).
	migrate := flag.Bool("migrate", false, "apply database migrations, then exit")
	flag.Parse()

	opts, err := app.Options(*migrate)
	if err != nil {
		fmt.Fprintln(os.Stderr, "startup:", err)
		os.Exit(1)
	}
	fx.New(opts...).Run()
}
