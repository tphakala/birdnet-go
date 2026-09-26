package classifier

// divideThreads distributes a total thread count among models.
// Each model gets at least 1 thread. The remainder is assigned to the first model
// in modelIDs by contract, so a caller that wants a specific model to absorb it
// passes that model first.
//
// Note: the orchestrator gives each model the full thread budget because
// inference is serialized (inferenceMu), so it never calls this. It is retained for
// testing and a potential future parallel-inference mode.
func divideThreads(total int, modelIDs []string) map[string]int {
	n := len(modelIDs)
	if n == 0 {
		return nil
	}
	if total < n {
		total = n
	}
	perModel := total / n
	remainder := total % n
	result := make(map[string]int, n)
	for _, id := range modelIDs {
		result[id] = perModel
	}
	result[modelIDs[0]] += remainder
	return result
}
