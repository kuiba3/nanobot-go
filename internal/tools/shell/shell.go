// Package shell implements the `exec` tool: bounded command execution inside
// the workspace with deny/allow patterns and optional bubblewrap sandboxing.
package shell

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/hkuds/nanobot-go/internal/config"
	"github.com/hkuds/nanobot-go/internal/security"
	"github.com/hkuds/nanobot-go/internal/tools"
)

// Options controls exec behavior.
type Options struct {
	Workspace        string
	TimeoutS         int
	MaxOutputChars   int
	DenyPatterns     []string
	AllowPatterns    []string
	RestrictWorkspace bool
	Sandbox          bool // use bwrap on linux when available
	AllowedEnvKeys   []string
}

// NewFromConfig builds Options from a ToolsConfig.
func NewFromConfig(cfg config.ExecToolConfig, workspace string, restrict bool) Options {
	return Options{
		Workspace:        workspace,
		TimeoutS:         cfg.TimeoutS,
		MaxOutputChars:   cfg.MaxOutputChars,
		DenyPatterns:     cfg.DenyPatterns,
		AllowPatterns:    cfg.AllowPatterns,
		RestrictWorkspace: restrict,
		Sandbox:          cfg.Sandbox,
		AllowedEnvKeys:   cfg.AllowedEnvKeys,
	}
}

// New returns the exec tool.
func New(opts Options) tools.Tool {
	if opts.TimeoutS <= 0 {
		opts.TimeoutS = 300
	}
	if opts.MaxOutputChars <= 0 {
		opts.MaxOutputChars = 10_000
	}
	denyRe := compilePatterns(opts.DenyPatterns)
	allowRe := compilePatterns(opts.AllowPatterns)
	return &execTool{
		Base: tools.Base{
			ToolName:        "exec",
			ToolDescription: "Execute a shell command inside the workspace and return combined stdout/stderr. Timeout and output size are bounded.",
			Params: &tools.Schema{
				Type: "object",
				Properties: map[string]*tools.Schema{
					"command":   {Type: "string", Description: "Shell command to run via bash -lc."},
					"timeout_s": {Type: "integer", Description: "Override per-call timeout (seconds)."},
				},
				Required: []string{"command"},
			},
		},
		opts:    opts,
		denyRe:  denyRe,
		allowRe: allowRe,
	}
}

type execTool struct {
	tools.Base
	opts    Options
	denyRe  []*regexp.Regexp
	allowRe []*regexp.Regexp
}

func compilePatterns(pats []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(pats))
	for _, p := range pats {
		if re, err := regexp.Compile(p); err == nil {
			out = append(out, re)
		}
	}
	return out
}

func (t *execTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	cmdStr := tools.ArgString(args, "command", "")
	if strings.TrimSpace(cmdStr) == "" {
		return "", fmt.Errorf("command must be non-empty")
	}
	for _, re := range t.denyRe {
		if re.MatchString(cmdStr) {
			return "", fmt.Errorf("command blocked by deny pattern: %s", re.String())
		}
	}
	if len(t.allowRe) > 0 {
		ok := false
		for _, re := range t.allowRe {
			if re.MatchString(cmdStr) {
				ok = true
				break
			}
		}
		if !ok {
			return "", fmt.Errorf("command not allowed by allow patterns")
		}
	}
	if t.opts.RestrictWorkspace && security.ContainsInternalURL(cmdStr) {
		return "", fmt.Errorf("command references internal URL")
	}

	timeout := t.opts.TimeoutS
	if v := tools.ArgInt(args, "timeout_s", 0); v > 0 {
		timeout = v
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	shell := "/bin/bash"
	shellArgs := []string{"-lc", cmdStr}
	if runtime.GOOS == "windows" {
		shell = "cmd"
		shellArgs = []string{"/c", cmdStr}
	}

	// Optional sandbox wrapping (linux bwrap)
	if t.opts.Sandbox && runtime.GOOS == "linux" {
		if _, err := exec.LookPath("bwrap"); err == nil {
			bwrap := []string{
				"bwrap",
				"--bind", t.opts.Workspace, t.opts.Workspace,
				"--ro-bind", "/usr", "/usr",
				"--ro-bind", "/bin", "/bin",
				"--ro-bind", "/lib", "/lib",
				"--ro-bind", "/lib64", "/lib64",
				"--ro-bind", "/etc", "/etc",
				"--proc", "/proc",
				"--dev", "/dev",
				"--chdir", t.opts.Workspace,
				shell, "-lc", cmdStr,
			}
			shell = bwrap[0]
			shellArgs = bwrap[1:]
		}
	}

	cmd := exec.CommandContext(cctx, shell, shellArgs...)
	cmd.Dir = t.opts.Workspace
	cmd.Env = t.filteredEnv()

	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	err := cmd.Run()

	out := combined.String()
	if len(out) > t.opts.MaxOutputChars {
		out = out[:t.opts.MaxOutputChars] + fmt.Sprintf("\n...[truncated at %d chars]", t.opts.MaxOutputChars)
	}
	if err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return out + "\n[killed: timeout]", nil
		}
		return fmt.Sprintf("exit code %v\n%s", err, out), nil
	}
	return out, nil
}

func (t *execTool) filteredEnv() []string {
	if len(t.opts.AllowedEnvKeys) == 0 {
		return os.Environ()
	}
	set := make(map[string]struct{}, len(t.opts.AllowedEnvKeys))
	for _, k := range t.opts.AllowedEnvKeys {
		set[k] = struct{}{}
	}
	out := make([]string, 0, len(set)+4)
	out = append(out, "PATH="+os.Getenv("PATH"), "HOME="+os.Getenv("HOME"), "USER="+os.Getenv("USER"), "LANG="+os.Getenv("LANG"))
	for _, e := range os.Environ() {
		if idx := strings.IndexByte(e, '='); idx > 0 {
			if _, ok := set[e[:idx]]; ok {
				out = append(out, e)
			}
		}
	}
	return out
}
