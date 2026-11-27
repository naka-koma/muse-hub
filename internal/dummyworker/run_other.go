//go:build !windows

package dummyworker

import (
	"context"
	"errors"
)

const Supported = false

func Run(context.Context) (string, error) {
	return "", errors.New("dummy process tree requires Windows")
}
func Command([]string) (int, bool) { return 0, false }

func NewCodex(string, string) (func(context.Context) (string, error), error) {
	return nil, errors.New("the Codex probe requires Windows")
}
