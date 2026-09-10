package store

// RuntimeStats 是设置页「运行时」区块要展示的计数。
type RuntimeStats struct {
	Works      int `json:"works"`
	Docs       int `json:"docs"`
	Revisions  int `json:"revisions"`
	Runs       int `json:"runs"`
	RunEvents  int `json:"runEvents"`
	RunningNow int `json:"runningNow"`
}

// RuntimeStats 统计各表规模与当前运行中的工单数。
func (s *Store) RuntimeStats() (RuntimeStats, error) {
	var out RuntimeStats
	counts := []struct {
		query string
		dst   *int
	}{
		{`SELECT COUNT(*) FROM works`, &out.Works},
		{`SELECT COUNT(*) FROM docs`, &out.Docs},
		{`SELECT COUNT(*) FROM revisions`, &out.Revisions},
		{`SELECT COUNT(*) FROM runs`, &out.Runs},
		{`SELECT COUNT(*) FROM run_events`, &out.RunEvents},
		{`SELECT COUNT(*) FROM runs WHERE status = 'running'`, &out.RunningNow},
	}
	for _, c := range counts {
		if err := s.DB.QueryRow(c.query).Scan(c.dst); err != nil {
			return out, err
		}
	}
	return out, nil
}
