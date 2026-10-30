//go:build !windows

package config

import "os"

func systemEnvironmentValue(name string) string {
	return os.Getenv(name)
}
