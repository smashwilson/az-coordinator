package cli

import (
	"flag"
	"os"

	log "github.com/sirupsen/logrus"
)

var commands = map[string]func(){
	"help":        help,
	"init":        initialize,
	"set-secrets": setSecrets,
	"diff":        diff,
	"sync":        sync,
	"serve":       serve,
}

// Launch parses and interprets CLI flags and performs the requested operation.
func Launch() {
	var (
		verbose = false
		quiet   = false
		help    = false
	)

	const verboseDescription = "Log everything that may be logged."
	flag.BoolVar(&verbose, "verbose", false, verboseDescription)
	flag.BoolVar(&verbose, "v", false, verboseDescription)

	const quietDescription = "Log only warnings and errors."
	flag.BoolVar(&quiet, "quiet", false, quietDescription)
	flag.BoolVar(&quiet, "q", false, quietDescription)

	const helpDescription = "Show this message."
	flag.BoolVar(&help, "help", false, helpDescription)
	flag.BoolVar(&help, "h", false, helpDescription)

	flag.Parse()

	if verbose && quiet {
		log.Error("-verbose and -quiet may not be provided together.")
		writeHelp(os.Stderr, 1)
	}

	if verbose {
		log.SetLevel(log.TraceLevel)
	}
	if quiet {
		log.SetLevel(log.WarnLevel)
	}

	if help {
		writeHelp(os.Stdout, 0)
	}

	if flag.NArg() < 1 {
		log.Error("You must provide at least one command.")
		writeHelp(os.Stderr, 1)
	}

	if fn, ok := commands[flag.Arg(0)]; ok {
		fn()
	} else {
		writeHelp(os.Stderr, 1)
	}
}
