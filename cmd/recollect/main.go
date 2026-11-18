// Command recollect serves the Recollect engine over stdio.
package main

import (
	"fmt"
	"os"

	"github.com/dkmnx/recollect/internal/rpc"
)

func main() {
	if err := rpc.Serve(os.Stdin, os.Stdout, rpc.Deps{}); err != nil {
		fmt.Fprintln(os.Stderr, "recollect:", err)
		os.Exit(1)
	}
}
