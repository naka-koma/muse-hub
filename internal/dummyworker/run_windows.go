package dummyworker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/nakakoma/muse-hub/internal/asyncjobs"
	"golang.org/x/sys/windows"
)

const Supported = true

const (
	parentArg = "--muse-hub-dummy-parent"
	leafArg   = "--muse-hub-dummy-leaf"
	// ProcThreadAttributeValue(13, FALSE, TRUE, FALSE), from WinBase.h.
	jobListAttribute = 0x0002000d
)

type processTree struct {
	job     windows.Handle
	process windows.Handle
	cancel  windows.Handle
}

// JOBOBJECT_BASIC_ACCOUNTING_INFORMATION is not exposed by x/sys/windows.
type jobAccounting struct {
	userTime, kernelTime, periodUser, periodKernel          int64
	pageFaults, totalProcesses, activeProcesses, terminated uint32
}

// Run executes only the built-in arithmetic fixture, never an MCP argument.
func Run(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	exe, err := os.Executable()
	if err != nil {
		return "", errors.New("could not locate the dummy worker")
	}
	tree, err := startTree(exe, 0, parentArg)
	if err != nil {
		return "", errors.New("could not start the isolated dummy process tree")
	}
	defer tree.close()
	exit, err := tree.wait(ctx)
	if err != nil {
		return "", err
	}
	if exit != childSucceeded {
		return "", errors.New("dummy process tree did not return the expected result")
	}
	return strconv.FormatUint(uint64(exit), 10), nil
}

// Command dispatches the fixed helper modes before the Hub loads credentials.
func Command(args []string) (int, bool) {
	switch {
	case len(args) == 2 && args[0] == parentArg:
		return runDummyParent(args[1], leafArg), true
	case len(args) == 6 && args[0] == codexParentArg:
		return runCodexParent(args[5], args[1], args[2], args[3], args[4]), true
	case len(args) == 6 && args[0] == claudeParentArg:
		return runClaudeParent(args[5], args[1], args[2], args[3], args[4]), true
	case len(args) == 1 && args[0] == leafArg:
		time.Sleep(5 * time.Second)
		return 2 + 3, true
	default:
		return 0, false
	}
}

const (
	parentCancelled = 2
	childSucceeded  = 5
)

func runDummyParent(cancelArg string, childArgs ...string) int {
	exe, err := os.Executable()
	if err != nil {
		return 1
	}
	return runParent(cancelArg, exec.Command(exe, childArgs...), func(err error) int {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == childSucceeded {
			return childSucceeded
		}
		return childFailed
	})
}

func runParent(cancelArg string, cmd *exec.Cmd, exitCode func(error) int) int {
	number, err := strconv.ParseUint(cancelArg, 10, strconv.IntSize)
	if err != nil || number == 0 {
		return 1
	}
	cancel := windows.Handle(number)
	defer windows.CloseHandle(cancel)
	initial, err := windows.WaitForSingleObject(cancel, 0)
	if err != nil {
		return 1
	}
	if initial == windows.WAIT_OBJECT_0 {
		return parentCancelled
	}
	// os/exec restricts inherited handles to the standard I/O handles it opens.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, NoInheritHandles: cmd.Stdin == nil}
	if err := cmd.Start(); err != nil {
		return 1
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case err := <-done:
			return exitCode(err)
		default:
		}
		status, err := windows.WaitForSingleObject(cancel, 15)
		if err != nil || status == windows.WAIT_OBJECT_0 {
			_ = cmd.Process.Kill()
			<-done
			if err == nil && cmd.ProcessState != nil && cmd.ProcessState.Exited() {
				return parentCancelled
			}
			return 1
		}
	}
}

// childFailed records a normal wait for a worker that exited unsuccessfully.
const childFailed = 3

func startTree(exe string, input windows.Handle, args ...string) (*processTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = windows.CloseHandle(job)
		}
	}()
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return nil, err
	}
	app, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return nil, err
	}
	security := windows.SecurityAttributes{InheritHandle: 1}
	security.Length = uint32(unsafe.Sizeof(security))
	cancel, err := windows.CreateEvent(&security, 1, 0, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if !keep {
			_ = windows.CloseHandle(cancel)
		}
	}()
	command := append([]string{exe}, args...)
	handles := []windows.Handle{cancel}
	if input != 0 {
		command = append(command, strconv.FormatUint(uint64(input), 10))
		handles = append(handles, input)
	}
	command = append(command, strconv.FormatUint(uint64(cancel), 10))
	line, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(command))
	if err != nil {
		return nil, err
	}
	systemRoot, err := windows.GetSystemWindowsDirectory()
	if err != nil {
		return nil, err
	}
	// Do not inherit Hub credentials or its environment overrides.
	env := utf16.Encode([]rune("SystemRoot=" + systemRoot + "\x00\x00"))
	attrs, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return nil, err
	}
	defer attrs.Delete()
	// Attach the job before the new process can create its own children.
	if err := attrs.Update(jobListAttribute, unsafe.Pointer(&job), unsafe.Sizeof(job)); err != nil {
		return nil, err
	}
	// Inherit only the cancellation event and the optional job input pipe.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(cancel)); err != nil {
		return nil, err
	}
	startup := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	var proc windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_NO_WINDOW | windows.CREATE_UNICODE_ENVIRONMENT)
	if err := windows.CreateProcess(app, line, nil, nil, true, flags, &env[0], nil, &startup.StartupInfo, &proc); err != nil {
		return nil, err
	}
	_ = windows.CloseHandle(proc.Thread)
	keep = true
	return &processTree{job: job, process: proc.Process, cancel: cancel}, nil
}

func (t *processTree) close() {
	_ = windows.CloseHandle(t.job)
	_ = windows.CloseHandle(t.process)
	_ = windows.CloseHandle(t.cancel)
}

func (t *processTree) active() (uint32, error) {
	var info jobAccounting
	if err := windows.QueryInformationJobObject(t.job, windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
		return 0, err
	}
	return info.activeProcesses, nil
}

func (t *processTree) waitEmpty(ctx context.Context) error {
	tick := time.NewTicker(15 * time.Millisecond)
	defer tick.Stop()
	for {
		active, err := t.active()
		if err != nil {
			return err
		}
		if active == 0 {
			status, err := windows.WaitForSingleObject(t.process, 0)
			if err != nil || status == windows.WAIT_OBJECT_0 {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func (t *processTree) wait(ctx context.Context) (uint32, error) {
	if err := t.waitEmpty(ctx); err != nil {
		if stopErr := t.stop(); stopErr != nil {
			return 0, stopErr
		}
		return 0, err
	}
	var exit uint32
	err := windows.GetExitCodeProcess(t.process, &exit)
	return exit, err
}

func (t *processTree) stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if windows.SetEvent(t.cancel) == nil && t.waitEmpty(ctx) == nil {
		var exit uint32
		if windows.GetExitCodeProcess(t.process, &exit) == nil {
			switch exit {
			case parentCancelled, childSucceeded, childFailed, codexRateLimit:
				return nil
			}
		}
	}
	// Force cleanup if the parent did not confirm that it waited for the leaf.
	// Closing the job is a second cleanup path, not evidence of a confirmed stop.
	_ = windows.TerminateJobObject(t.job, 1)
	return asyncjobs.ErrStopUnconfirmed
}
