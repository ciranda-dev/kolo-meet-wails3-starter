package main

import "testing"

func TestSetMemoryLimit(t *testing.T) {
	var set []int64
	record := func(n int64) int64 { set = append(set, n); return 12345 }

	limit, fromEnv := setMemoryLimit(func(string) string { return "" }, record)
	if limit != defaultMemoryLimit || fromEnv || len(set) != 1 || set[0] != defaultMemoryLimit {
		t.Errorf("no GOMEMLIMIT: limit %d from env %v, set %v", limit, fromEnv, set)
	}
	if defaultMemoryLimit < 512<<20 {
		t.Errorf("the default %d is below 512 MiB", defaultMemoryLimit)
	}

	set = nil
	limit, fromEnv = setMemoryLimit(func(k string) string {
		if k == "GOMEMLIMIT" {
			return "1GiB"
		}
		return ""
	}, record)
	if limit != 12345 || !fromEnv || len(set) != 1 || set[0] != -1 {
		t.Errorf("GOMEMLIMIT set: limit %d from env %v, set %v (the app must only read the limit)", limit, fromEnv, set)
	}
}
