//go:build !windows

package main

func newDesktop() (desktop, error) {
	return nil, errDesktopUnsupported
}
