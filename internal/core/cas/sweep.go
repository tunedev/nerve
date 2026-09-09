package cas

// Retain runs keep with sweeps of this store excluded, so an object stored
// inside keep is still stored when keep returns. Content is put before whatever
// will name it exists, and a sweep landing in that window deletes an object a
// caller is about to claim.
//
// The exclusion belongs to the store because the store is what a sweep empties.
// A caller holding a lock of its own excludes only itself, and callers sharing
// one store is the arrangement a sweep has to survive. Like the local ref
// store's compare-and-set, this holds for this process and for no other.
//
// Two Retains do not exclude each other. Content addressing is what makes that
// safe: two callers storing at once write the same bytes to the same path.
func (s *Store) Retain(keep func() error) error {
	s.sweeps.RLock()
	defer s.sweeps.RUnlock()
	return keep()
}

// Sweep runs remove with every Retain over this store excluded, which is what
// makes removing safe while the store is in use. What to remove stays the
// caller's: the store does not know what still refers to an object.
func (s *Store) Sweep(remove func() error) error {
	s.sweeps.Lock()
	defer s.sweeps.Unlock()
	return remove()
}
