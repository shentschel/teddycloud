package main

import (
	"encoding/json"
	"fmt"
	"os"

	apphealth "github.com/shentschel/teddycloud/next/backend/internal/application/health"
)

const version = "0.0.0-dev"

func main() {
	status := apphealth.New(version).Current()
	if err := json.NewEncoder(os.Stdout).Encode(status); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
