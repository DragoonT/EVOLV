package genome

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// SearchMatch represents a playbook ranked by relevance to a query.
type SearchMatch struct {
	Playbook Playbook `json:"playbook"`
	Score    float64  `json:"score"`
	Matches  []string `json:"matches"` // matched query terms
}

// Search ranks playbooks by lexical relevance using Okapi BM25 scoring with
// outcome weighting. When a query is empty, it returns the newest verified playbooks.
func (b *Book) Search(query string, limit int) []SearchMatch {
	if limit <= 0 {
		limit = 5
	}
	if len(b.Playbooks) == 0 {
		return nil
	}

	terms := tokenize(query)
	if len(terms) == 0 {
		var out []SearchMatch
		for _, p := range b.Playbooks {
			out = append(out, SearchMatch{Playbook: p, Score: 1.0})
			if len(out) >= limit {
				break
			}
		}
		return out
	}

	type docEntry struct {
		playbook Playbook
		tf       map[string]float64
		length   float64
	}

	N := float64(len(b.Playbooks))
	docs := make([]docEntry, len(b.Playbooks))
	df := make(map[string]float64)
	totalLen := 0.0

	for i, p := range b.Playbooks {
		tf := make(map[string]float64)
		docTokens := 0.0

		addWeighted := func(text string, weight float64) {
			toks := tokenize(text)
			for _, tok := range toks {
				tf[tok] += weight
				docTokens += 1.0
			}
		}

		addWeighted(p.Title, 3.0)
		addWeighted(p.Notes, 1.5)
		addWeighted(p.Verify, 2.0)
		for _, s := range p.Skills {
			addWeighted(s, 1.5)
		}
		for _, t := range p.Tools {
			addWeighted(t, 1.5)
		}
		for _, tr := range p.Traits {
			addWeighted(tr, 1.0)
		}

		if docTokens == 0 {
			docTokens = 1.0
		}
		totalLen += docTokens
		docs[i] = docEntry{playbook: p, tf: tf, length: docTokens}

		for term := range tf {
			df[term]++
		}
	}

	avgdl := totalLen / N
	if avgdl <= 0 {
		avgdl = 1.0
	}

	k1 := 1.2
	bParam := 0.75

	var matches []SearchMatch
	for _, doc := range docs {
		score := 0.0
		var matchedTerms []string

		for _, q := range terms {
			freq, ok := doc.tf[q]
			if !ok || freq <= 0 {
				continue
			}
			matchedTerms = append(matchedTerms, q)
			nQ := df[q]
			// Lucene / Okapi BM25 IDF: ln(1 + (N - n + 0.5) / (n + 0.5))
			idf := math.Log(1.0 + (N-nQ+0.5)/(nQ+0.5))
			if idf < 0.1 {
				idf = 0.1
			}
			num := freq * (k1 + 1.0)
			denom := freq + k1*(1.0-bParam+bParam*(doc.length/avgdl))
			score += idf * (num / denom)
		}

		if score <= 0 || len(matchedTerms) == 0 {
			continue
		}

		// Domain weighting: favor verified playbooks (what actually worked)
		switch doc.playbook.Outcome {
		case "verified":
			score *= 1.3
			if doc.playbook.Attempts == 1 {
				score *= 1.1
			}
		case "unverified":
			score *= 1.0
		case "failed":
			score *= 0.7
		}

		// Breadth bonus: reward hitting multiple query terms
		overlapRatio := float64(len(matchedTerms)) / float64(len(terms))
		score *= (1.0 + 0.5*overlapRatio)

		matches = append(matches, SearchMatch{
			Playbook: doc.playbook,
			Score:    math.Round(score*100) / 100,
			Matches:  matchedTerms,
		})
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Playbook.At > matches[j].Playbook.At
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

// tokenize extracts lowercase alphanumeric words and filters stopwords.
func tokenize(text string) []string {
	var words []string
	var cur strings.Builder
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else {
			if cur.Len() > 0 {
				w := cur.String()
				if len(w) >= 2 && !stopwords[w] {
					words = append(words, w)
				}
				cur.Reset()
			}
		}
	}
	if cur.Len() > 0 {
		w := cur.String()
		if len(w) >= 2 && !stopwords[w] {
			words = append(words, w)
		}
	}
	return words
}

var stopwords = map[string]bool{
	"a": true, "about": true, "above": true, "after": true, "again": true,
	"against": true, "all": true, "am": true, "an": true, "and": true,
	"any": true, "are": true, "as": true, "at": true, "be": true,
	"because": true, "been": true, "before": true, "being": true,
	"below": true, "between": true, "both": true, "but": true, "by": true,
	"can": true, "did": true, "do": true, "does": true, "doing": true,
	"down": true, "during": true, "each": true, "few": true, "for": true,
	"from": true, "further": true, "had": true, "has": true, "have": true,
	"having": true, "he": true, "her": true, "here": true, "hers": true,
	"herself": true, "him": true, "himself": true, "his": true, "how": true,
	"i": true, "if": true, "in": true, "into": true, "is": true, "it": true,
	"its": true, "itself": true, "just": true, "me": true, "more": true,
	"most": true, "my": true, "myself": true, "no": true, "nor": true,
	"not": true, "now": true, "of": true, "off": true, "on": true,
	"once": true, "only": true, "or": true, "other": true, "our": true,
	"ours": true, "ourselves": true, "out": true, "over": true, "own": true,
	"same": true, "she": true, "should": true, "so": true, "some": true,
	"such": true, "than": true, "that": true, "the": true, "their": true,
	"theirs": true, "them": true, "themselves": true, "then": true,
	"there": true, "these": true, "they": true, "this": true, "those": true,
	"through": true, "to": true, "too": true, "under": true, "until": true,
	"up": true, "very": true, "was": true, "we": true, "were": true,
	"what": true, "when": true, "where": true, "which": true, "while": true,
	"who": true, "whom": true, "why": true, "with": true, "would": true,
	"you": true, "your": true, "yours": true, "yourself": true, "yourselves": true,
}
