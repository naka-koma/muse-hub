package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/nakakoma/muse-hub/internal/asyncjobs"
	"github.com/nakakoma/muse-hub/internal/policy"
)

var errPolicyUsage = errors.New("usage: muse-hub policy current|list|weekly or muse-hub policy approve|reject PROPOSAL_ID REVISION")

// The local command has no HTTP entry point. The exclusive job DB lock also
// prevents a policy change while another Hub process is serving this policy.
func runPolicyCommand(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 {
		return errPolicyUsage
	}
	switch args[0] {
	case "current", "list", "weekly":
		if len(args) != 1 {
			return errPolicyUsage
		}
	case "approve", "reject":
		if len(args) != 3 {
			return errPolicyUsage
		}
	default:
		return errPolicyUsage
	}
	path, err := jobsDBSetting()
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("set MUSE_HUB_JOBS_DB to the existing Hub job database")
	}
	ctx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	manager, err := asyncjobs.Open(ctx, path, 30*time.Second)
	if err != nil {
		return errors.New("cannot open the job database; stop the Hub before using local policy commands")
	}
	defer manager.Close()
	var result any
	switch args[0] {
	case "current":
		var current *policy.Policy
		if current, err = manager.ManagedPolicy(ctx); err == nil {
			result = struct {
				Revision string        `json:"revision"`
				Policy   policy.Config `json:"policy"`
			}{current.Revision(), current.Config()}
		}
	case "list":
		result, err = manager.PolicyProposals(ctx)
	case "weekly":
		result, err = manager.WeeklyPolicy(ctx)
	case "approve", "reject":
		result, err = manager.DecidePolicy(ctx, args[1], args[2], args[0] == "approve")
	}
	if err != nil {
		return err
	}
	render := json.NewEncoder(output)
	render.SetIndent("", "  ")
	return render.Encode(result)
}
