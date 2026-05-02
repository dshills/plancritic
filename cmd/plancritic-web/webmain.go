package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/dshills/plancritic/internal/reviewer"
)

var version = "0.1.0"

func main() {
	root := newServeCmd()
	root.Use = "plancritic-web"
	root.Short = "Run the PlanCritic HTMX web UI"
	root.Version = version
	root.SilenceErrors = true
	root.SilenceUsage = true

	if err := root.Execute(); err != nil {
		var ee *reviewer.Error
		if errors.As(err, &ee) {
			fmt.Fprintln(os.Stderr, ee.Msg)
			os.Exit(ee.Code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
