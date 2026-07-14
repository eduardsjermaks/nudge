package tier1

// Distance computes the restricted Damerau-Levenshtein (optimal string
// alignment) distance: insertions, deletions, substitutions, and adjacent
// transpositions. Transpositions matter because they are the most common
// keyboard slip (pshu -> push).
func Distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	prev2 := make([]int, lb+1)
	prev := make([]int, lb+1)
	cur := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			m := prev[j] + 1 // deletion
			if v := cur[j-1] + 1; v < m {
				m = v // insertion
			}
			if v := prev[j-1] + cost; v < m {
				m = v // substitution
			}
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				if v := prev2[j-2] + 1; v < m {
					m = v // transposition
				}
			}
			cur[j] = m
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[lb]
}

// maxDistFor is the confidence bar: how far a candidate may be from the
// typed word and still count as a "pure typo". Deliberately strict — anything
// fuzzier belongs to the LLM tier.
func maxDistFor(word string) int {
	switch n := len([]rune(word)); {
	case n < 3:
		return 0 // too short to guess safely
	case n <= 6:
		return 1
	default:
		return 2
	}
}

// bestMatch returns the unique nearest candidate within the confidence bar,
// or "" if there is none or the best is ambiguous (two candidates tied).
// Distances are scored in half-steps (scaled x2): a scrambled-letters slip
// (same letter multiset at edit distance 2, like pshu -> push) scores 1.5 —
// inside the bar, but strictly worse than a clean single edit, so gti still
// resolves to git even though tig is an anagram too.
func bestMatch(word string, candidates []string) string {
	limit := maxDistFor(word)
	if limit == 0 {
		return ""
	}
	scaledLimit := limit*2 + 1 // allow the x.5 anagram score at the bar
	best, bestScore, ties := "", scaledLimit+1, 0
	for _, c := range candidates {
		if c == word {
			return "" // the word is already valid
		}
		// Cheap length filter before the O(n*m) distance.
		if dl := len(c) - len(word); dl > limit+1 || -dl > limit+1 {
			continue
		}
		d := Distance(word, c)
		score := d * 2
		if d == 2 && sameRunes(word, c) {
			score = 3
		}
		switch {
		case score < bestScore:
			best, bestScore, ties = c, score, 1
		case score == bestScore && c != best:
			ties++
		}
	}
	if bestScore > scaledLimit || ties != 1 {
		return ""
	}
	return best
}
