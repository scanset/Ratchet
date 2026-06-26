package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/scanset/Ratchet/internal/instance"
	"github.com/scanset/Ratchet/internal/jsonx"
	"github.com/scanset/Ratchet/internal/model"
)

func cmdList(args []string) int {
	dir := arg(args, 1)
	if dir == "" {
		return fail(fmt.Errorf("usage: ratchet list <dir> [--group G] [--type T] [--json]"))
	}
	group, typ, asJSON := "", "", false
	for i := 2; i < len(args); i++ {
		switch {
		case args[i] == "--group" && i+1 < len(args):
			group = args[i+1]
			i++
		case args[i] == "--type" && i+1 < len(args):
			typ = args[i+1]
			i++
		case args[i] == "--json":
			asJSON = true
		}
	}
	inst, err := instance.Open(dir)
	if err != nil {
		return fail(err)
	}
	if inst.Manifest == nil {
		fmt.Println("no manifest.json in " + inst.Root)
		return 0
	}
	var entries []model.Entry
	for _, e := range inst.Manifest.Entries {
		if group != "" && !strings.EqualFold(e.Group, group) {
			continue
		}
		if typ != "" && !strings.EqualFold(e.DocType, typ) {
			continue
		}
		entries = append(entries, e)
	}

	if asJSON {
		var arr []any
		for _, e := range entries {
			kws := e.Keywords
			if kws == nil {
				kws = []string{}
			}
			arr = append(arr, jsonx.Obj("id", e.ID, "title", e.Title, "group", e.Group,
				"doc_type", e.DocType, "path", e.Path, "summary", e.Summary, "keywords", kws))
		}
		fmt.Println(jsonx.SerializePretty(arr))
		return 0
	}

	sort.Slice(entries, func(i, j int) bool {
		gi, gj := strings.ToLower(entries[i].Group), strings.ToLower(entries[j].Group)
		if gi != gj {
			return gi < gj
		}
		return strings.ToLower(entries[i].ID) < strings.ToLower(entries[j].ID)
	})
	cur := " "
	for _, e := range entries {
		g := e.Group
		if g == "" {
			g = "(top level)"
		}
		if g != cur {
			if cur != " " {
				fmt.Println()
			}
			fmt.Println("[" + g + "]")
			cur = g
		}
		fmt.Println("  " + pad(e.ID, 24) + " " + e.Summary)
	}
	plural := "entries"
	if len(entries) == 1 {
		plural = "entry"
	}
	fmt.Printf("\n%d %s\n", len(entries), plural)
	return 0
}
