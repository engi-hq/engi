package trigger

// Leading reports whether this process leads and has loaded its schedules.
func (s *Service) Leading() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.leading && s.ready
}
