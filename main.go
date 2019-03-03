package main

import (
	"log"
)

func main() {
	s, err := newServer()
	if err != nil {
		log.Fatalf("Unable to create server: %v.\n", err)
	}

	if err := s.listen(); err != nil {
		log.Fatal(err)
	}
}
