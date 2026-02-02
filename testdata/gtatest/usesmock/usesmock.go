// Only the test file imports testmock
package usesmock

// Service provides real functionality
type Service struct{}

// DoSomething does real work
func (s *Service) DoSomething() string {
	return "real"
}
