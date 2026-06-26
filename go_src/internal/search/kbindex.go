// KbIndex - search over a knowledge-base DIRECTORY (the kb/ or recipes/ bucket, a registered KB, or
// an ad-hoc path). It walks the directory into chunks (one per text file), BM25-ranks them with the
// shared core in Search, and returns grounding text or a hits list. A registered KB caches its built
// corpus under the instance's .index/ (keyed by name, invalidated by a cheap file-count + mtime
// fingerprint). Port of src.bak/Runtime/KbIndex.cs.
//
// cacheDir is the resolved .index directory ("" disables caching, e.g. an ad-hoc path); cacheKey is
// the KB name used for the cache filename ("" disables caching).
package search

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/scanset/Ratchet/internal/jsonx"
	"github.com/scanset/Ratchet/internal/meta"
)

// BuildCorpus builds the corpus for a directory: one chunk per text file (id = forward-slashed
// relative path). Skips build/cache/vcs folders.
func BuildCorpus(dirAbs string) []Doc {
	var docs []Doc
	st, err := os.Stat(dirAbs)
	if dirAbs == "" || err != nil || !st.IsDir() {
		return docs
	}
	root, _ := filepath.Abs(dirAbs)
	walkCorpus(root, root, &docs)
	return docs
}

func walkCorpus(root, dir string, docs *[]Doc) {
	files, _ := os.ReadDir(dir)
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Name()) < strings.ToLower(files[j].Name()) })
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(f.Name()))
		if !kbTextExt[ext] {
			continue
		}
		full := filepath.Join(dir, f.Name())
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		text := meta.StripMeta(string(data)) // drop any legacy block so it doesn't pollute title/search
		rel := relForward(root, full)
		*docs = append(*docs, Doc{
			ID:    rel,
			Kind:  strings.TrimPrefix(ext, "."),
			Text:  text,
			Title: kbTitle(text, strings.TrimSuffix(f.Name(), filepath.Ext(f.Name()))),
		})
	}
	subs, _ := os.ReadDir(dir)
	sort.Slice(subs, func(i, j int) bool { return strings.ToLower(subs[i].Name()) < strings.ToLower(subs[j].Name()) })
	for _, s := range subs {
		if s.IsDir() && !kbSkipDirs[strings.ToLower(s.Name())] {
			walkCorpus(root, filepath.Join(dir, s.Name()), docs)
		}
	}
}

func kbTitle(text, fallback string) string {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
		if strings.HasPrefix(line, "#") {
			t := strings.TrimSpace(strings.TrimLeft(line, "#"))
			if t != "" {
				return t
			}
		}
		if line != "" {
			if len(line) > 80 {
				return line[:80]
			}
			return line // first non-empty line
		}
	}
	return fallback
}

// Query searches a knowledge base directory. hitsOnly returns just the locations (path + title).
func Query(cacheDir, cacheKey, dirAbs, query string, k int, hitsOnly bool) string {
	st, err := os.Stat(dirAbs)
	if dirAbs == "" || err != nil || !st.IsDir() {
		return "(knowledge base not found: " + dirAbs + ")"
	}
	docs := loadCorpus(cacheDir, cacheKey, dirAbs)
	if len(docs) == 0 {
		return "(no indexable files under " + dirAbs + ")"
	}
	scored := Bm25Scored(docs, query)
	if len(scored) == 0 {
		return "(no matches for '" + query + "')"
	}
	var sb strings.Builder
	shown := 0
	for _, s := range scored {
		if shown >= k {
			break
		}
		shown++
		d := docs[s.Index]
		if hitsOnly {
			sb.WriteString(d.ID + "  -  " + d.Title + "\n")
		} else {
			sb.WriteString("## " + d.Title + "  (" + d.ID + ")\n" + d.Text + "\n\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// Rank returns the top-k file paths (relative ids) ranked for the query, via the same cached corpus +
// BM25 as Query. The narrowing step for /search dispatch.
func Rank(cacheDir, cacheKey, dirAbs, query string, k int) []string {
	var out []string
	st, err := os.Stat(dirAbs)
	if dirAbs == "" || err != nil || !st.IsDir() {
		return out
	}
	docs := loadCorpus(cacheDir, cacheKey, dirAbs)
	if len(docs) == 0 {
		return out
	}
	scored := Bm25Scored(docs, query)
	for i := 0; i < len(scored) && len(out) < k; i++ {
		out = append(out, docs[scored[i].Index].ID)
	}
	return out
}

// --- corpus cache (registered KBs only) ---

func loadCorpus(cacheDir, cacheKey, dirAbs string) []Doc {
	if cacheDir == "" || cacheKey == "" {
		return BuildCorpus(dirAbs) // ad-hoc: no cache
	}
	fp := fingerprint(dirAbs)
	cachePath := filepath.Join(cacheDir, safeKey(cacheKey)+".json")

	if data, err := os.ReadFile(cachePath); err == nil {
		if root := jsonx.AsObject(parseOrNil(data)); root != nil && jsonx.GetStringOr(root, "fp", "") == fp {
			var docs []Doc
			for _, o := range jsonx.GetArr(root, "docs") {
				dd := jsonx.AsObject(o)
				if dd == nil {
					continue
				}
				docs = append(docs, Doc{
					ID:    jsonx.GetStringOr(dd, "id", ""),
					Title: jsonx.GetStringOr(dd, "title", ""),
					Kind:  jsonx.GetStringOr(dd, "kind", ""),
					Text:  jsonx.GetStringOr(dd, "text", ""),
				})
			}
			return docs
		}
	}

	built := BuildCorpus(dirAbs)
	arr := make([]any, 0, len(built))
	for _, d := range built {
		arr = append(arr, jsonx.Obj("id", d.ID, "title", d.Title, "kind", d.Kind, "text", d.Text))
	}
	if err := os.MkdirAll(cacheDir, 0o755); err == nil {
		_ = os.WriteFile(cachePath, []byte(jsonx.Serialize(jsonx.Obj("fp", fp, "docs", arr))), 0o644)
	}
	return built
}

// fingerprint is a cheap staleness key: indexed-file count + the latest write time (metadata only).
func fingerprint(dirAbs string) string {
	var count, maxNanos int64
	fingerprintWalk(dirAbs, &count, &maxNanos)
	return strconv.FormatInt(count, 10) + ":" + strconv.FormatInt(maxNanos, 10)
}

func fingerprintWalk(dir string, count, maxNanos *int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !kbTextExt[strings.ToLower(filepath.Ext(e.Name()))] {
			continue
		}
		*count++
		if info, err := e.Info(); err == nil {
			if t := info.ModTime().UnixNano(); t > *maxNanos {
				*maxNanos = t
			}
		}
	}
	for _, e := range entries {
		if e.IsDir() && !kbSkipDirs[strings.ToLower(e.Name())] {
			fingerprintWalk(filepath.Join(dir, e.Name()), count, maxNanos)
		}
	}
}

func safeKey(key string) string {
	var sb strings.Builder
	for _, c := range key {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			sb.WriteRune(c)
		} else {
			sb.WriteByte('_')
		}
	}
	return sb.String()
}
