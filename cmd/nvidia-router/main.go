package main

import (
	"os"

	"nvidia-router/internal/app"
)

func main() {
	app.RunCLI(os.Args[1:])
}
