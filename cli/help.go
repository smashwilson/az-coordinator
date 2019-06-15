package cli

import "os"

func help() {
	writeHelp(os.Stdout, 0)
}
