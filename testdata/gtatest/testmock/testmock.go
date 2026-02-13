// This package should only be imported by test files
package testmock

// MockService is a mock implementation for testing
type MockService struct{}

// DoSomething is a mock method
func (m *MockService) DoSomething() string {
	return "mocked"
}
