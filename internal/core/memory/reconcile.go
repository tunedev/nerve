package memory

// question is what two facts have to share to be two answers to one thing: the
// category the fact was filed under, and the subject it names. Both are read as
// text, the way a claim is.
type question struct {
	kind    string
	subject string
}

// questionOf reads the question a fact answers, and reports whether it answers
// one at all.
func questionOf(f Fact) (question, bool) {
	subject := fold(f.Subject)
	if subject == "" {
		return question{}, false
	}
	return question{kind: fold(f.Kind), subject: subject}, true
}

// entry is one memory about to be written: the fact, and the sequences of the
// memories it replaces.
type entry struct {
	fact     Fact
	replaced []int
}

// lastAnswers returns the batch with every answer to a question dropped except
// the last one given, in the order the rest were given. A conversation that
// answered one question twice ended on its second answer, and that is what it
// left the workspace believing.
func lastAnswers(facts []Fact) []Fact {
	last := make(map[question]int, len(facts))
	for i, fact := range facts {
		if q, answers := questionOf(fact); answers {
			last[q] = i
		}
	}
	kept := make([]Fact, 0, len(facts))
	for i, fact := range facts {
		if q, answers := questionOf(fact); answers && last[q] != i {
			continue
		}
		kept = append(kept, fact)
	}
	return kept
}

// reconcile pairs each fact with the believed memories it contradicts: every
// one answering the question it answers.
//
// Restatements are expected to be gone already, so a believed memory answering
// the same question says something else and the fact replaces it. A workspace
// believing two answers to one question stops believing both.
func reconcile(believed []Memory, facts []Fact) []entry {
	answered := make(map[question][]int, len(believed))
	for _, m := range believed {
		if q, answers := questionOf(m.Fact); answers {
			answered[q] = append(answered[q], m.Seq)
		}
	}
	entries := make([]entry, len(facts))
	for i, fact := range facts {
		entries[i] = entry{fact: fact}
		if q, answers := questionOf(fact); answers {
			entries[i].replaced = answered[q]
		}
	}
	return entries
}
