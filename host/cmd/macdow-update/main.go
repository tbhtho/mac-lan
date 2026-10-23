package main

import (
	"github.com/tbhtho/MAC-DOW/host/internal/updater"
	"os"
)

func main() { os.Exit(updater.Run(os.Args[1:], os.Stdout)) }
