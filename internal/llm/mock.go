package llm

import "context"

// MockProvider is a test double that returns canned responses.
type MockProvider struct {
	Response string
	Err      error
	// Calls counts Generate invocations so tests can assert that a
	// code path did (or did not) reach the provider.
	Calls int
}

func (m *MockProvider) Name() string { return "mock" }

func (m *MockProvider) Generate(_ context.Context, _ string, _ Settings) (string, Usage, error) {
	m.Calls++
	return m.Response, Usage{}, m.Err
}
