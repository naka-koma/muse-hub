package dummyworker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/windows"
)

const (
	workerAnswer    = "answer.txt"
	maxWorkerOutput = 64 * 1024
)

func runProfileWorker(ctx context.Context, parentArg, binary, profile, prompt string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, errors.New("could not locate the worker")
	}
	dir, err := os.MkdirTemp("", "muse-hub-worker-")
	if err != nil {
		return nil, errors.New("could not create the worker directory")
	}
	defer os.RemoveAll(dir)
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, errors.New("could not create the worker input pipe")
	}
	defer writer.Close()
	handle := windows.Handle(reader.Fd())
	if err := windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
		_ = reader.Close()
		return nil, errors.New("could not prepare the worker input pipe")
	}
	tree, err := startTree(exe, handle, parentArg, binary, dir, profile)
	_ = reader.Close()
	if err != nil {
		return nil, errors.New("could not start the worker process tree")
	}
	defer tree.close()
	written := make(chan error, 1)
	go func() {
		_, err := io.WriteString(writer, prompt)
		written <- errors.Join(err, writer.Close())
	}()
	exit, err := tree.wait(ctx)
	if err != nil {
		return nil, err
	}
	if exit == codexRateLimit {
		return nil, errors.New("Codex rate limit (HTTP 429)")
	}
	if exit != childSucceeded {
		return nil, fmt.Errorf("worker helper exited with status %d", exit)
	}
	if <-written != nil {
		return nil, errors.New("worker did not receive its input")
	}
	file, err := os.Open(filepath.Join(dir, workerAnswer))
	if err != nil {
		return nil, errors.New("worker did not produce an answer")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxWorkerOutput+1))
	if err != nil || len(data) > maxWorkerOutput {
		return nil, errors.New("could not read the worker answer within its size limit")
	}
	return data, nil
}

func workerInput(arg string) *os.File {
	handle, err := strconv.ParseUint(arg, 10, strconv.IntSize)
	if err != nil || handle == 0 {
		return nil
	}
	return os.NewFile(uintptr(handle), "worker-input")
}
