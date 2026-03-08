package fetcher

import (
	"fmt"
	"sync"
)

// Call records a single command invocation.
type Call struct {
	Name string
	Args []string
}

// MockRunner records command calls and returns configurable results.
// Use it in tests to avoid running real helm commands.
type MockRunner struct {
	mu sync.Mutex

	Calls []Call

	// RunFunc, if set, is called for Run(). If nil, Run returns RunErr.
	RunFunc func(name string, args ...string) error
	RunErr  error

	// OutputFunc, if set, is called for Output(). If nil, Output returns OutputData/OutputErr.
	OutputFunc func(name string, args ...string) ([]byte, error)
	OutputData []byte
	OutputErr  error
}

func (m *MockRunner) Run(name string, args ...string) error {
	m.mu.Lock()
	m.Calls = append(m.Calls, Call{Name: name, Args: args})
	m.mu.Unlock()

	if m.RunFunc != nil {
		return m.RunFunc(name, args...)
	}
	return m.RunErr
}

func (m *MockRunner) Output(name string, args ...string) ([]byte, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, Call{Name: name, Args: args})
	m.mu.Unlock()

	if m.OutputFunc != nil {
		return m.OutputFunc(name, args...)
	}
	return m.OutputData, m.OutputErr
}

// CallArgs returns the args for the n-th call (0-indexed), or nil if out of range.
func (m *MockRunner) CallArgs(n int) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n >= len(m.Calls) {
		return nil
	}
	return m.Calls[n].Args
}

// CallCount returns the number of recorded calls.
func (m *MockRunner) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Calls)
}

// HasCallWithArgs returns true if any recorded call matches the given args sequence.
func (m *MockRunner) HasCallWithArgs(args ...string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.Calls {
		if argsMatch(c.Args, args) {
			return true
		}
	}
	return false
}

func argsMatch(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// FailOnNthRun returns a RunFunc that fails on the n-th call (1-indexed) with
// the given error, and succeeds on all others.
func FailOnNthRun(n int, err error) func(string, ...string) error {
	count := 0
	return func(name string, args ...string) error {
		count++
		if count == n {
			return err
		}
		return nil
	}
}

// FailOnArgsContaining returns a RunFunc that fails when any arg contains substr.
func FailOnArgsContaining(substr string, err error) func(string, ...string) error {
	return func(name string, args ...string) error {
		for _, arg := range args {
			if contains(arg, substr) {
				return err
			}
		}
		return nil
	}
}

func contains(s, substr string) bool {
	return fmt.Sprintf("%s", s) != "" && len(s) >= len(substr) && searchSubstr(s, substr)
}

func searchSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
