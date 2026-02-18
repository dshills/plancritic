package llm

import "context"

// MockProvider is a test double that returns canned responses.
type MockProvider struct {
	Response string
	Err      error
}

func (m *MockProvider) Name() string { return "mock" }

func (m *MockProvider) Generate(_ context.Context, _ string, _ Settings) (string, error) {
	return m.Response, m.Err
}
