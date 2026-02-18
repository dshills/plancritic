package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "0.1.0"

func main() {
	root := &cobra.Command{
		Use:     "plancritic",
		Short:   "Review software implementation plans for contradictions, ambiguities, and risks",
		Version: version,
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	root.AddCommand(newCheckCmd())

	if err := root.Execute(); err != nil {
		var ee *exitErr
		if errors.As(err, &ee) {
			fmt.Fprintln(os.Stderr, ee.msg)
			os.Exit(ee.code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
