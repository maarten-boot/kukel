package eventsmod

// HeapLen is the number of timers in the loop's heap, cancelled or not.
func HeapLen(l *Loop) int { return len(l.timers) }
