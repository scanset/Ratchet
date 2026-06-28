// Package search provides retrieval: the shared BM25-lite ranking core (this file), per-KB corpus
// indexing with an on-disk cache (kbindex.go), deterministic manifest generation (indexer.go), and
// embedding rerank (embedder.go). Port of src.bak/Runtime/{Search,KbIndex,Indexer,Embedder}.cs.
//
// To keep the package a leaf (no import of internal/instance), the cache-aware entry points take a
// resolved cache directory string rather than an *Instance; the caller resolves it via the sandbox.
package search

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

// Doc is a single searchable chunk: an id (a relative file path), a title, a kind, and the body text
// BM25 ranks on.
type Doc struct {
	ID    string
	Title string
	Kind  string
	Text  string
}

// Scored is one (doc index, score) result with score > 0.
type Scored struct {
	Index int
	Score float64
}

var tokRe = regexp.MustCompile(`[a-z0-9_]+`)

const (
	bm25K1 = 1.5
	bm25B  = 0.75
)

// Bm25Scored scores a doc list against a query, returning (index, score) pairs with score > 0, sorted
// by score descending.
func Bm25Scored(docs []Doc, query string) []Scored {
	var scores []Scored
	n := len(docs)
	if n == 0 {
		return scores
	}

	tf := make([]map[string]int, n)
	length := make([]int, n)
	df := map[string]int{}
	for i := 0; i < n; i++ {
		toks := Tokens(docs[i].Title + " " + docs[i].Text)
		h := map[string]int{}
		for _, w := range toks {
			h[w]++
		}
		tf[i] = h
		length[i] = len(toks)
		for w := range h {
			df[w]++
		}
	}
	avgdl := 0.0
	for i := 0; i < n; i++ {
		avgdl += float64(length[i])
	}
	if n > 0 {
		avgdl /= float64(n)
	}
	if avgdl == 0 {
		avgdl = 1
	}

	qset := map[string]bool{}
	for _, w := range Tokens(query) {
		qset[w] = true
	}
	for i := 0; i < n; i++ {
		s := 0.0
		dl := float64(length[i])
		for w := range qset {
			f, ok := tf[i][w]
			if !ok {
				continue
			}
			dfw := df[w]
			idf := math.Log(1 + (float64(n)-float64(dfw)+0.5)/(float64(dfw)+0.5))
			s += idf * (float64(f) * (bm25K1 + 1)) / (float64(f) + bm25K1*(1-bm25B+bm25B*dl/avgdl))
		}
		if s > 0 {
			scores = append(scores, Scored{Index: i, Score: s})
		}
	}
	sort.SliceStable(scores, func(a, b int) bool { return scores[a].Score > scores[b].Score })
	return scores
}

// Tokens lowercases s, splits it into [a-z0-9_]+ tokens, and light-stems each so word forms collapse
// to a shared root (channels->channel, matches->match, indexing->index). Applied to both query and doc
// text in Bm25Scored, so retrieval is self-consistent. The go ratchet's route_score oracle mirrors
// these exact stem rules so the routing gate predicts what retrieval does.
func Tokens(s string) []string {
	var out []string
	if s == "" {
		return out
	}
	for _, m := range tokRe.FindAllString(strings.ToLower(s), -1) {
		out = append(out, stem(m))
	}
	return out
}

// stemSuffixes are tried in order; the first match with at least 3 letters remaining is stripped.
// Light by design (not a full Porter stemmer): enough to fold plural/verb forms, mirrored verbatim in
// tools/route_score.sh. BM25's IDF already handles common words, so no stopword list is needed here.
var stemSuffixes = []string{"izations", "ization", "ies", "ing", "edly", "ed"}

// stem folds word forms to a shared root. Plurals: "es" is a real suffix only after a sibilant
// (boxes->box, matches->match); otherwise just the trailing "s" is removed (goroutines->goroutine,
// names->name) so singular and plural agree. Mirror tools/route_score.sh exactly if this changes.
func stem(t string) string {
	for _, suf := range stemSuffixes {
		if len(t)-len(suf) >= 3 && strings.HasSuffix(t, suf) {
			if suf == "ies" {
				return t[:len(t)-len(suf)] + "y"
			}
			return t[:len(t)-len(suf)]
		}
	}
	if len(t) >= 5 && strings.HasSuffix(t, "es") {
		base := t[:len(t)-2]
		if strings.HasSuffix(base, "ch") || strings.HasSuffix(base, "sh") ||
			strings.IndexByte("sxz", base[len(base)-1]) >= 0 {
			return base // boxes->box, dishes->dish, buzzes->buzz
		}
		return t[:len(t)-1] // goroutines->goroutine, names->name
	}
	if len(t) >= 4 && strings.HasSuffix(t, "s") && !strings.HasSuffix(t, "ss") {
		return t[:len(t)-1] // channels->channel, pods->pod
	}
	return t
}
