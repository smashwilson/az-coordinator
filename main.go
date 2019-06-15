package main

import (
	_ "github.com/lib/pq"
	"github.com/smashwilson/az-coordinator/cli"
)

func main() {
	cli.Launch()
}
