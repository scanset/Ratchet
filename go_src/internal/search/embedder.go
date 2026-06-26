// Embedder - the ICM "embedder" role: narrow a candidate set to the top-k most similar to a query, so
// the constrained model pick (flow router, KB route) chooses from a short, relevant list instead of
// the whole catalog. The embedder NEVER decides or generates - it only ranks. Falls back to "use all
// candidates" (returns nil) whenever the embed model is absent or Ollama is unreachable. Vectors are
// cached (model-keyed, keyed by candidate id + a content hash so edits re-embed). Port of
// src.bak/Runtime/Embedder.cs.
package search

import (
	"math"
	"os"
	"path/filepath"
	"strconv"

	"github.com/scanset/Ratchet/internal/jsonx"
	"github.com/scanset/Ratchet/internal/ollama"
)

const embCacheFile = ".emb_cache.routing.json"

// Cand is a ranking candidate: an id and the text to embed.
type Cand struct {
	ID   string
	Text string
}

// VecCand is a candidate paired with its already-computed vector (for the pure ranking core).
type VecCand struct {
	ID  string
	Vec []float64
}

// RankTopK returns the top-k candidate ids by cosine similarity to the query, in rank order. Returns
// nil if embeddings are unavailable (the caller should then use all candidates). cacheDir is the
// resolved .index directory ("" disables the vector cache).
func RankTopK(cacheDir, url, model, query string, cands []Cand, k int, status func(string)) []string {
	if model == "" || len(cands) == 0 {
		return nil
	}
	cache := loadEmbCache(cacheDir, model)
	qv, err := ollama.Embed(url, model, query, nil)
	if err != nil || len(qv) == 0 {
		if status != nil && err != nil {
			status("embedder unavailable (" + err.Error() + "); using all candidates")
		}
		return nil
	}

	dirty := false
	vecs := make([]VecCand, 0, len(cands))
	for _, c := range cands {
		key := c.ID + "#" + hash(c.Text)
		cv, ok := cache[key]
		if !ok || cv == nil {
			cv, err = ollama.Embed(url, model, c.Text, nil)
			if err != nil {
				if status != nil {
					status("embedder unavailable (" + err.Error() + "); using all candidates")
				}
				return nil
			}
			cache[key] = cv
			dirty = true
		}
		vecs = append(vecs, VecCand{ID: c.ID, Vec: cv})
	}
	if dirty {
		saveEmbCache(cacheDir, model, cache)
	}
	return RankByVectors(qv, vecs, k)
}

// RankByVectors ranks already-computed vectors by cosine similarity to q (testable without Ollama).
func RankByVectors(q []float64, cands []VecCand, k int) []string {
	type scored struct {
		id    string
		score float64
	}
	list := make([]scored, len(cands))
	for i, c := range cands {
		list[i] = scored{c.ID, Cosine(q, c.Vec)}
	}
	// stable sort by score desc, matching the C# List.Sort comparison
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j].score > list[j-1].score; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
	out := []string{}
	for i := 0; i < len(list) && i < k; i++ {
		out = append(out, list[i].id)
	}
	return out
}

// Cosine returns the cosine similarity of a and b (0 when either is zero-length or zero-norm).
func Cosine(a, b []float64) float64 {
	if a == nil || b == nil {
		return 0
	}
	dot, na, nb := 0.0, 0.0, 0.0
	m := len(a)
	if len(b) < m {
		m = len(b)
	}
	for i := 0; i < m; i++ {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// hash is a stable content hash (FNV-1a, hex) so a cache key changes when the candidate text changes.
func hash(s string) string {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return zeroPadHex(h)
}

func zeroPadHex(h uint32) string {
	s := strconv.FormatUint(uint64(h), 16)
	for len(s) < 8 {
		s = "0" + s
	}
	return s
}

func embCachePath(cacheDir string) string { return filepath.Join(cacheDir, embCacheFile) }

func loadEmbCache(cacheDir, model string) map[string][]float64 {
	cache := map[string][]float64{}
	if cacheDir == "" {
		return cache
	}
	data, err := os.ReadFile(embCachePath(cacheDir))
	if err != nil {
		return cache
	}
	root := jsonx.AsObject(parseOrNil(data))
	if root == nil || jsonx.GetStringOr(root, "model", "") != model {
		return cache // model-keyed
	}
	if vecs := jsonx.GetObject(root, "vecs"); vecs != nil {
		for k, v := range vecs {
			cache[k] = toFloats(jsonx.AsArr(v))
		}
	}
	return cache
}

func saveEmbCache(cacheDir, model string, cache map[string][]float64) {
	if cacheDir == "" {
		return
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return
	}
	vecs := make(map[string]any, len(cache))
	for k, v := range cache {
		vecs[k] = v
	}
	_ = os.WriteFile(embCachePath(cacheDir), []byte(jsonx.Serialize(jsonx.Obj("model", model, "vecs", vecs))), 0o644)
}

func toFloats(nums []any) []float64 {
	out := make([]float64, len(nums))
	for i, n := range nums {
		if d, ok := jsonx.ToDouble(n); ok {
			out[i] = d
		}
	}
	return out
}
