//go:build windows

package service

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isPlatformPersistentTransportError(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED) ||
		errors.Is(err, windows.WSAEHOSTUNREACH) ||
		errors.Is(err, windows.WSAENETUNREACH)
}
