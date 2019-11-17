package snd

import (
	"os"
	"strings"
)

func isWavFile(deviceOrFile string) bool {
	if strings.HasSuffix(strings.ToLower(deviceOrFile), ".wav") {
		return true
	}
	if strings.Contains(deviceOrFile, "/") {
		return true
	}
	if _, err := os.Stat(deviceOrFile); err == nil {
		return true
	}
	return false
}
