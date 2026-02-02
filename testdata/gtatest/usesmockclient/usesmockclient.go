// Package usesmockclient imports usesmock for production use
// It does not import testmock at all (neither in production nor tests)
package usesmockclient

import "gta.test/usesmock"

// Client uses the real service.
type Client struct {
	svc *usesmock.Service
}

// New creates a new Client.
func New() *Client {
	return &Client{svc: &usesmock.Service{}}
}

// Run uses the service.
func (c *Client) Run() string {
	return c.svc.DoSomething()
}
