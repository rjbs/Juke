// Command juke is the Juke server and its tools.
//
//	juke import --library DIR --catalog FILE --out FILE [--include NAME ...]
//	juke check  --db FILE --library DIR
//	juke serve  --db FILE --library DIR [--listen ADDR]
//	            [--tailnet NAME --tailnet-state DIR --allow LOGIN ...]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx := context.Background()
	var err error
	switch os.Args[1] {
	case "import":
		err = runImport(ctx, os.Args[2:])
	case "check":
		err = runCheck(ctx, os.Args[2:])
	case "serve":
		err = runServe(ctx, os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "juke:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: juke import|check|serve [options]; juke COMMAND -h for options")
	os.Exit(2)
}

// listFlag collects a repeatable string flag.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

func parse(fs *flag.FlagSet, args []string, required ...string) {
	fs.Parse(args)
	for _, name := range required {
		if fs.Lookup(name).Value.String() == "" {
			fmt.Fprintf(os.Stderr, "juke %s: --%s is required\n", fs.Name(), name)
			fs.Usage()
			os.Exit(2)
		}
	}
}
