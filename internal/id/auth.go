package id

import "github.com/engi-hq/engi/internal/domain"

// UserID returns a new user ID.
func (g *Generator) UserID() (domain.UserID, error) {
	s, err := g.New(domain.PrefixUser)
	return domain.UserID(s), err
}

// APIKeyID returns a new API key ID.
func (g *Generator) APIKeyID() (domain.APIKeyID, error) {
	s, err := g.New(domain.PrefixAPIKey)
	return domain.APIKeyID(s), err
}
