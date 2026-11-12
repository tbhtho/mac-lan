//go:build !windows

package main

import "errors"

func startDetached(...string) error { return errors.New("only used on Windows") }
