# Knowledge bases (how the kb works, and building one)

A knowledge base (kb) is indexed reference content the local model retrieves from, scoped per step
(Context Binding). It grounds generation in real material - API signatures, error explanations, design
patterns - so a small model answers from sources instead of guessing.

## How it is organized

- A **library** is a directory of markdown files plus a generated `manifest.json` (the routing index).
- A ratchet can have several libraries, each registered in `ratchet.json` under `knowledgeBases[]`:

```json
"knowledgeBases": [
  { "name": "kb",   "path": "kb",          "default": true },
  { "name": "docs", "path": "kb/external" }
]
```

`path` may point anywhere (inside or outside the ratchet). One library can be the `default` - searched
when a query names no library.

Alternatively (and **preferred when present**), a ratchet registers its libraries in a top-level
`kb/catalog.json` - an entry list `{ "name", "path", "default"[, "summary"] }`. The engine reads
`kb/catalog.json` as the registry if it exists and falls back to `knowledgeBases[]` otherwise. This keeps
`ratchet.json` thin, co-locates the registry with the libraries it describes, and lets a ratchet tool
auto-generate and maintain it (discover the `kb/*` dirs, keep the authored `summary` lines). `doctor`
validates every registered library either way.

## How a topic is indexed

One topic per markdown file under the library directory. When you index, each file becomes one entry:

- the **first heading** (`# ...`) becomes the entry's `title`,
- the **first prose line** becomes the `summary`,
- the top terms become the `keywords`.

Routing matches a query against the title, summary, and keywords, so **make those first two lines sharp** -
they are what retrieval sees. `README.md` folder guides are skipped. An indexed entry looks like:

```json
{ "id": "reference-example-topic", "title": "Example reference topic",
  "path": "reference/example-topic.md",
  "summary": "One-line summary taken from the first prose line.",
  "keywords": ["query", "search", "binding", "..."] }
```

Build (or rebuild) the index after adding or editing topics:

```
ratchet index <ratchet>\kb\<lib>      # writes kb/<lib>/manifest.json
```

## How retrieval works

By default retrieval is **BM25** (keyword scoring over the manifest). The tokenizer lowercases and
light-stems, so plural/verb forms fold together (`channels` ~ `channel`, `goroutines` ~ `goroutine`) on
both the query and the indexed text. `ratchet tokenize` exposes that exact tokenizer (stdin lines ->
token lines) so a tool can fold words identically to retrieval instead of reimplementing it. If the
ratchet sets an **embed** model seat, the embedder **re-ranks** the BM25 hits semantically (vectors
cached in `.index/`). Either way the result is the top-k entries' content, capped, injected into the slot
you bound it to.

## How a flow uses the kb

A node grounds on a library through an input binding:

- **`search`** - a templated query, top-k hits into a slot:
  `{ "search": "kb", "query": "{{ task }}", "k": 2, "as": "refs", "max_chars": 2000 }`
- **`ref`** - a single fixed entry, always present (no query).

The **plan-routed** pattern (used by the cpp and dotnet flows) routes retrieval precisely: a `plan` node is
a `generate` with an `output_schema` that emits one query field per library (empty = skip); downstream
`search` bindings pull each field via the binding `path`. The model decides which libraries the task
needs. See [Author flows](author-flows.md) and [Context Binding](../concepts/context-binding.md).

## Build a new kb for a ratchet

1. **Create the library.** `kb/<lib>/`, one topic per `.md` file. Make the first heading and the first
   prose line sharp - they become the routing title and summary.
2. **Index it.** `ratchet index <ratchet>\kb\<lib>` -> writes `kb/<lib>/manifest.json`. Re-run after edits.
3. **Register it.** Add `{ "name": "<lib>", "path": "kb/<lib>" }` to `knowledgeBases[]` in `ratchet.json`
   (one library may be `default`) - or, if the ratchet uses a `kb/catalog.json` registry, add the entry
   there (or run the ratchet's catalog tool to auto-discover it).
4. **Wire it in.** Add a `search` or `ref` binding to the flow nodes that should ground on it (or the
   plan-routed pattern for multi-library routing).
5. **Verify.** `ratchet doctor <ratchet>` checks the library (manifest present, entry count matches), then
   test with `/search [<lib>] <query>` in the console.

Worked example: `RatchetBox/Windows/cpp` ships seven libraries (`cppref`, `guidelines`, `patterns`, `errors`,
`cppdocs`, `win32`, `howto`), each registered and plan-routed.
