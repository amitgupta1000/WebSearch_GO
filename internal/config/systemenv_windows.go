//go:build windows

package config

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

func systemEnvironmentValue(name string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}

	for _, scope := range []struct {
		root registry.Key
		path string
	}{
		{registry.CURRENT_USER, `Environment`},
		{registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`},
	} {
		key, err := registry.OpenKey(scope.root, scope.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		value, _, err := key.GetStringValue(name)
		_ = key.Close()
		if err == nil && value != "" {
			return value
		}
	}

	return ""
}
