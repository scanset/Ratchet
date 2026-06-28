// Package conventions holds the ratchet file/dir layout and the string identifiers the engine
// dispatches on (intents, tool kinds, action-node kinds). Centralizing them kills typo drift and
// documents, in one place, exactly what a ratchet directory may contain and what the runtime
// understands. Port of src.bak/Conventions.cs.
package conventions

// Top-level files a ratchet may provide.
const (
	ConfigFile       = "ratchet.json"    // the launch config (references dirs)
	LegacyConfigFile = "icm.config.json" // oldest per-instance config (still opened)
	ManifestFile     = "manifest.json"   //
	SystemFile       = "SYSTEM.md"       //
	NotesFile        = "NOTES.md"        // persistent session memory the chat reads/appends
	KnowledgeFile    = "knowledge.json"  // live-read KB registry (name -> path), never cached
	GlobalConfigFile = "icm.global.json" // host-level base config (beside the exe, or $ICM_GLOBAL)
)

// ConfigCandidates are the config filenames tried in order when opening a directory: the current
// name, then legacy names.
var ConfigCandidates = []string{ConfigFile, "icm.json", LegacyConfigFile}

// Sub-directories (relative to the instance root).
const (
	SchemasDir    = "schemas"
	SamplesDir    = "samples"
	FlowsDir      = "flows"
	KbDir         = "kb"
	KbCatalogFile = "catalog.json" // kb/catalog.json: the high-level KB registry (name/path/default/summary)
	RecipesDir    = "recipes"       // recipe bucket (prompt + bound flow/tool); also a knowledge bucket
	ToolsDir      = "tools"
	WorkspacesDir = "workspaces" // container of project workspaces (replaces out/)
	IndexDir      = ".index"     // per-instance search-index cache (keyed by KB name)
	RunsDir       = "runs"       // per-chain-run state (runs/<id>/step-NNN.json)
)

// Run-record file/dir names (under RunsDir).
const (
	RunsIndexFile  = "index.json"       // runs/index.json: one summary entry per run
	SnapshotSubdir = "workspace-before" // runs/<id>/workspace-before: the rollback source copy
)

// SchemaRel/SampleRel/FlowRel build the relative paths for the table/flow conventions.
func SchemaRel(table string) string { return SchemasDir + "/" + table + ".json" }
func SampleRel(table string) string { return SamplesDir + "/" + table + ".txt" }
func FlowRel(name string) string    { return FlowsDir + "/" + name + ".json" }

// A routable reference file leads with a metadata block in an HTML comment (invisible in rendered
// markdown, parseable): <!--icm { "id","title","doc_type","summary","keywords","source" } -->.
// `ratchet reindex` reads these to (re)generate manifest.json mechanically.
const (
	MetaOpen  = "<!--icm"
	MetaClose = "-->"
)

// RoutableDirs are the folders scanned for routable reference files (markdown with a metadata block).
var RoutableDirs = []string{"reference", "patterns", "recipes", "scaffold", "snippets", "kb"}

// Intent values: the dispatcher's constrained classify enum.
const (
	IntentAsk      = "ask"
	IntentMake     = "make"
	IntentValidate = "validate"
	IntentPropose  = "propose"
	IntentHelp     = "help"
	IntentQuit     = "quit"
)

// IntentAll is the full intent set.
var IntentAll = []string{IntentAsk, IntentMake, IntentValidate, IntentPropose, IntentHelp, IntentQuit}

// Tool kinds the host knows how to dispatch (a command/script tool uses any other kind that declares
// a command/script).
const (
	ToolKindValidate       = "validate"
	ToolKindKbAnswer       = "kb_answer"
	ToolKindPropose        = "propose"
	ToolKindGenerateVerify = "generate_verify"
	ToolKindCommand        = "command"
	ToolKindScript         = "script"
)

// Action-chain node kinds (the flow model: flows/<chain>/actions/<a>/action.json).
const (
	ActionKindAction     = "action"     // deterministic side-effect (a tools/ script) + validators
	ActionKindGenerate   = "generate"   // free-text generation via the generate seat
	ActionKindAiBranch   = "ai_branch"  // slots -> prompt -> enum decision -> transitions
	ActionKindSummarizer = "summarizer" // deterministic transform of prior outputs
	ActionKindForEach    = "foreach"    // run a sub-chain once per item in a list slot (fan-out)
	ActionKindExit       = "exit"       // terminal outcome
)

// ActionKindAll is the full set of recognized node kinds (used by the linter).
var ActionKindAll = []string{
	ActionKindAction, ActionKindGenerate, ActionKindAiBranch,
	ActionKindSummarizer, ActionKindForEach, ActionKindExit,
}
