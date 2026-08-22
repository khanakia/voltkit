package publish

import (
	"errors"
	"os"
	"path/filepath"
)

var errTransient = errors.New("transient publish failure")

func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}

func base(p string) string { return filepath.Base(p) }
