package hooks

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"
)

// HookResult contains the result of a hook execution
type HookResult struct {
	Hook     Hook
	Phase    HookPhase
	Success  bool
	Stdout   string
	Stderr   string
	Duration time.Duration
	Error    error
}

// Executor runs hooks with proper environment and timeout handling
type Executor struct {
	config  *Config
	context ExportContext
	results []HookResult
	logger  func(string)
}

// NewExecutor creates a new hook executor
func NewExecutor(config *Config, ctx ExportContext) *Executor {
	return &Executor{
		config:  config,
		context: ctx,
		results: make([]HookResult, 0),
		logger:  func(string) {}, // No-op default
	}
}

// SetLogger sets the logger function for hook execution details
func (e *Executor) SetLogger(logger func(string)) {
	if logger == nil {
		e.logger = func(string) {}
		return
	}
	e.logger = logger
}

// RunPreExport executes all pre-export hooks
// Returns error if any hook fails with on_error="fail"
func (e *Executor) RunPreExport() error {
	if e.config == nil {
		return nil
	}

	for _, hook := range e.config.Hooks.PreExport {
		e.logger(fmt.Sprintf("Running pre-export hook %q: %s", hook.Name, hook.Command))
		result := e.runHook(hook, PreExport)
		e.results = append(e.results, result)

		if !result.Success && hook.OnError == "fail" {
			return fmt.Errorf("pre-export hook %q failed: %w", hook.Name, result.Error)
		}
	}

	return nil
}

// RunPostExport executes all post-export hooks
// Errors are logged but don't fail (unless on_error="fail")
func (e *Executor) RunPostExport() error {
	if e.config == nil {
		return nil
	}

	var firstError error
	for _, hook := range e.config.Hooks.PostExport {
		e.logger(fmt.Sprintf("Running post-export hook %q: %s", hook.Name, hook.Command))
		result := e.runHook(hook, PostExport)
		e.results = append(e.results, result)

		if !result.Success && hook.OnError == "fail" && firstError == nil {
			firstError = fmt.Errorf("post-export hook %q failed: %w", hook.Name, result.Error)
		}
	}

	return firstError
}

// getShellCommand returns the shell and flag to use for executing commands
func getShellCommand() (string, string) {
	if runtime.GOOS == "windows" {
		return "cmd", "/C"
	}
	return "sh", "-c"
}

// runHook executes a single hook with timeout and environment
func (e *Executor) runHook(hook Hook, phase HookPhase) HookResult {
	result := HookResult{
		Hook:  hook,
		Phase: phase,
	}

	start := time.Now()

	// Create context with timeout
	timeout := hook.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Create command - use shell to interpret the command
	shell, flag := getShellCommand()
	cmd := exec.CommandContext(ctx, shell, flag, hook.Command)

	// Build environment.
	// The full (unfiltered) environment is used only for ${VAR} expansion in
	// hook-specific env declarations; the subprocess itself gets a scrubbed
	// environment with credential-bearing variables removed. Hooks live in the
	// project's .bv/hooks.yaml, which may come from an untrusted repository, so
	// ambient tokens must not leak into them by default. A hook that
	// legitimately needs a credential can re-grant it explicitly, e.g.:
	//   env:
	//     GITHUB_TOKEN: "${GITHUB_TOKEN}"
	fullEnv := append(os.Environ(), e.context.ToEnv()...)
	cmd.Env = scrubEnviron(os.Environ())

	// Add export context variables
	cmd.Env = append(cmd.Env, e.context.ToEnv()...)

	// Add hook-specific env vars (with ${VAR} expansion from current env)
	// Sort keys for deterministic environment order
	envKeys := make([]string, 0, len(hook.Env))
	for k := range hook.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)

	expansionEnv := fullEnv
	for _, key := range envKeys {
		value := hook.Env[key]
		// Use custom expansion that sees both OS env and context variables,
		// including scrubbed credentials (explicit re-grant by the hook author),
		// plus hook env vars declared earlier in sorted order.
		expandedValue := expandEnv(value, expansionEnv)
		pair := fmt.Sprintf("%s=%s", key, expandedValue)
		cmd.Env = append(cmd.Env, pair)
		expansionEnv = append(expansionEnv, pair)
	}

	// Capture output
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Run the command
	err := cmd.Run()
	result.Duration = time.Since(start)
	result.Stdout = strings.TrimSpace(stdout.String())
	result.Stderr = strings.TrimSpace(stderr.String())

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.Error = fmt.Errorf("timeout after %v", timeout)
		} else {
			result.Error = err
		}
		result.Success = false
	} else {
		result.Success = true
	}

	return result
}

// sensitiveEnvMarkers are substrings that, when present in an (upper-cased)
// environment variable name, mark it as credential-bearing. Matching variables
// are stripped from hook subprocess environments; see runHook for the explicit
// re-grant mechanism.
var sensitiveEnvMarkers = []string{
	"TOKEN",
	"SECRET",
	"PASSWORD",
	"PASSWD",
	"CREDENTIAL",
	"API_KEY",
	"APIKEY",
	"ACCESS_KEY",
	"PRIVATE_KEY",
}

// sensitiveEnvExact are exact environment variable names that are stripped from
// hook subprocess environments in addition to marker matches.
var sensitiveEnvExact = map[string]bool{
	"SSH_AUTH_SOCK": true, // forwarding the SSH agent grants signing/auth capability
}

// isSensitiveEnvKey reports whether an environment variable name looks like it
// carries a credential and should be withheld from project-controlled hooks.
func isSensitiveEnvKey(key string) bool {
	upper := strings.ToUpper(key)
	if sensitiveEnvExact[upper] {
		return true
	}
	for _, marker := range sensitiveEnvMarkers {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

// scrubEnviron returns a copy of env with credential-bearing variables removed.
func scrubEnviron(env []string) []string {
	scrubbed := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, ok := strings.Cut(kv, "=")
		if ok && isSensitiveEnvKey(key) {
			continue
		}
		scrubbed = append(scrubbed, kv)
	}
	return scrubbed
}

// expandEnv replaces ${VAR} or $VAR in the string using values from the env slice
// Note: This only supports standard shell variable expansion. It does not support
// complex shell parameter expansion like ${VAR:-default} or ${VAR:offset}.
func expandEnv(s string, env []string) string {
	return os.Expand(s, func(key string) string {
		// Search env slice from end to beginning (to respect precedence if duplicates exist)
		prefix := key + "="
		for i := len(env) - 1; i >= 0; i-- {
			if strings.HasPrefix(env[i], prefix) {
				return env[i][len(prefix):]
			}
		}
		return ""
	})
}

// Results returns all hook execution results
func (e *Executor) Results() []HookResult {
	return e.results
}

// Summary returns a human-readable summary of hook execution
func (e *Executor) Summary() string {
	if len(e.results) == 0 {
		return "No hooks executed"
	}

	var sb strings.Builder
	var succeeded, failed int

	for _, r := range e.results {
		if r.Success {
			succeeded++
			sb.WriteString(fmt.Sprintf("  [OK] %s (%v)\n", r.Hook.Name, r.Duration.Round(time.Millisecond)))
		} else {
			failed++
			sb.WriteString(fmt.Sprintf("  [FAIL] %s: %v\n", r.Hook.Name, r.Error))
			if r.Stderr != "" {
				sb.WriteString(fmt.Sprintf("         stderr: %s\n", truncate(r.Stderr, 200)))
			}
		}
	}

	header := fmt.Sprintf("Hook execution: %d succeeded, %d failed\n", succeeded, failed)
	return header + sb.String()
}

// truncate shortens a string to max length with ellipsis
func truncate(s string, max int) string {
	if max < 0 {
		max = 0
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

// RunHooks is a convenience function that runs all hooks for an export operation
func RunHooks(projectDir string, ctx ExportContext, noHooks bool) (*Executor, error) {
	if noHooks {
		return nil, nil
	}

	loader := NewLoader(WithProjectDir(projectDir))
	if err := loader.Load(); err != nil {
		return nil, fmt.Errorf("loading hooks: %w", err)
	}

	if !loader.HasHooks() {
		return nil, nil
	}

	executor := NewExecutor(loader.Config(), ctx)
	return executor, nil
}
