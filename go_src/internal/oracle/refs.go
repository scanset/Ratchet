package oracle

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/scanset/Ratchet/internal/instance"
	"github.com/scanset/Ratchet/internal/model"
)

// BuildRefs builds the cross-table refs map for an instance: every schema in schemas/ that declares a
// key, paired with the id set from its samples/<table>.txt. Keyed by schema name (what a ref column's
// ref_table points at). Tables with no sample contribute nothing. Port of Oracle.BuildRefs.
func BuildRefs(inst *instance.Instance) RefSet {
	refs := RefSet{}
	dir := inst.SchemasDirAbs()
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return refs
	}
	for _, f := range files {
		table := strings.TrimSuffix(filepath.Base(f), ".json")
		schema, err := model.LoadTableSchema(f)
		if err != nil || schema.Key == "" {
			continue
		}
		sample := inst.SamplePath(table)
		data, err := os.ReadFile(sample)
		if err != nil {
			continue
		}
		refs[schema.Name] = IdSet(schema, string(data))
	}
	return refs
}
