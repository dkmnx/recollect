// fact_links.go — link resolution. Links are stored slugged; resolution is
// one hop against the live store, root first when names collide. Dangling
// targets are data, not errors: readers skip them and /memory rebuild
// reports them.

package memory

// ResolveLinks returns the facts name's links point at, in link order,
// plus the targets that resolve to nothing. One hop, so cycles cannot
// occur.
func (s *Store) ResolveLinks(name string) ([]Fact, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fact, err := s.findFactLocked(FactSlug(name))
	if err != nil {
		return nil, nil, err
	}
	if fact == nil {
		return nil, nil, Errorf(CodeFactNotFound, "no fact named %q", name)
	}
	if len(fact.Links) == 0 {
		return nil, nil, nil
	}
	facts, err := s.listFactsLocked()
	if err != nil {
		return nil, nil, err
	}
	var related []Fact
	var dangling []string
	for _, link := range fact.Links {
		if match := findFactIn(facts, link); match != nil {
			related = append(related, *match)
		} else {
			dangling = append(dangling, link)
		}
	}
	return related, dangling, nil
}

// equalLinks reports whether two normalized link lists carry the same
// names in the same order.
func equalLinks(a, b []string) bool {
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

// CountDanglingLinks counts distinct link targets that name no fact across
// the whole project, for the /memory rebuild report.
func (s *Store) CountDanglingLinks() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	facts, err := s.listFactsLocked()
	if err != nil {
		return 0, err
	}
	names := make(map[string]bool, len(facts))
	for i := range facts {
		names[facts[i].Name] = true
	}
	dangling := map[string]bool{}
	for i := range facts {
		for _, link := range facts[i].Links {
			if !names[link] {
				dangling[link] = true
			}
		}
	}
	return len(dangling), nil
}
