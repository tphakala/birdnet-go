package classifier

// divideThreads distributes a total thread count among models.
// Each model gets at least 1 thread. The remainder goes to the primary model.
// Callers must ensure primaryID is present in modelIDs.
func divideThreads(total int, modelIDs []string, primaryID string) map[string]int {
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
	result[primaryID] += remainder
	return result
}
