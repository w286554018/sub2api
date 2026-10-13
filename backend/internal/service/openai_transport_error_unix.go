//go:build !windows

package service

func isPlatformPersistentTransportError(error) bool { return false }
