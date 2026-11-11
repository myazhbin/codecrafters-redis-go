package main

// waitQueues holds, for each key, the clients that are blocked until
// something happens to that key. Within a queue, the client that has been
// waiting the longest comes first. W is what represents a blocked client.
//
// Keys without waiting clients have no queue at all.
type waitQueues[W comparable] map[string][]W

// add puts w at the back of the queue of each of the keys, which must not
// contain duplicates.
func (q waitQueues[W]) add(w W, keys []string) {
	for _, key := range keys {
		q[key] = append(q[key], w)
	}
}

// remove takes w out of the queue of each of the keys.
func (q waitQueues[W]) remove(w W, keys []string) {
	for _, key := range keys {
		waiters := q[key]
		for i, other := range waiters {
			if other == w {
				waiters = append(waiters[:i], waiters[i+1:]...)
				break
			}
		}
		if len(waiters) == 0 {
			delete(q, key)
		} else {
			q[key] = waiters
		}
	}
}

// unique returns keys without its duplicates, keeping the first occurrence of
// each key.
func unique(keys []string) []string {
	seen := make(map[string]bool, len(keys))
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	return out
}
