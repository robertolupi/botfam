// Package interp wraps the Mangle engine. It is the single place that imports
// the engine, keeping it swappable behind the fact schema (wiki
// CattleInvariantsAsLogic: keep the schema engine-neutral).
package interp

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"codeberg.org/TauCeti/mangle-go/ast"
	"codeberg.org/TauCeti/mangle-go/interpreter"
)

var headRe = regexp.MustCompile(`(?m)^([a-zA-Z_][a-zA-Z0-9_]*)\s*\(`)

// Result holds the rows of one queried predicate.
type Result struct {
	Predicate string
	Rows      []string
}

// Run loads ruleFile + storeFile, evaluates, and queries every head predicate
// in ruleFile whose name starts with prefix. progress (may be nil) receives
// engine diagnostics. Returns results, engine wall-clock, error. The engine
// has known panics (e.g. temporal+aggregation); they are recovered as errors.
func Run(ruleFile, storeFile, prefix string, progress io.Writer) (results []Result, dur time.Duration, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("mangle engine panic: %v", r)
		}
	}()

	if progress == nil {
		progress = io.Discard
	}
	preds, perr := headPredicates(ruleFile, prefix)
	if perr != nil {
		return nil, 0, perr
	}

	i := interpreter.New(progress, "", nil)
	load := ruleFile
	if storeFile != "" {
		load = ruleFile + "," + storeFile
	}
	start := time.Now()
	if e := i.Load(load); e != nil {
		return nil, 0, fmt.Errorf("load %s: %w", load, e)
	}
	for _, p := range preds {
		atom, e := i.ParseQuery(p)
		if e != nil {
			// predicate declared/headed but no facts derived -> empty result
			results = append(results, Result{Predicate: p})
			continue
		}
		facts, e := i.Query(atom)
		if e != nil {
			return nil, 0, fmt.Errorf("query %s: %w", p, e)
		}
		rows := make([]string, 0, len(facts))
		for _, f := range facts {
			switch v := f.(type) {
			case ast.Atom:
				rows = append(rows, v.DisplayString())
			case ast.TemporalAtom:
				rows = append(rows, v.DisplayString())
			default:
				rows = append(rows, f.String())
			}
		}
		sort.Strings(rows)
		results = append(results, Result{Predicate: p, Rows: rows})
	}
	dur = time.Since(start)
	return results, dur, nil
}

func headPredicates(ruleFile, prefix string) ([]string, error) {
	b, err := os.ReadFile(ruleFile)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range headRe.FindAllStringSubmatch(string(b), -1) {
		name := m[1]
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}
