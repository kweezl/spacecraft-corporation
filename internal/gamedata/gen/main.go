// Command gen generates a versioned game-data layer (internal/gamedata/db/<vN>)
// from a parsed spacecraft-resources directory, and copies the kept item icons
// into the emoji module's assets.
//
// It is a dev-only tool, driven by `go generate` from internal/gamedata. The
// source directory is the generated/ folder of the public resources repo
// (https://github.com/kweezl/spacecraft-resources), passed via the GAMEDATA_SOURCE
// env var (or -source). The generated Go is committed; CI never runs this.
//
// Usage:
//
//	GAMEDATA_SOURCE=/path/to/spacecraft-resources/generated go run ./gen -version v1
//
// For a breaking game update, cut the next layer on top of the current one:
//
//	go run ./gen -version v2 -parent v1
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/kweezl/spacecraft-corporation/internal/gamedata/schema"
)

func main() {
	version := flag.String("version", "", "target version directory, e.g. v1 (required)")
	parent := flag.String("parent", "", "parent version for a delta layer (empty = base)")
	srcFlag := flag.String("source", "", "resources dir (default $GAMEDATA_SOURCE)")
	root := flag.String("root", ".", "the internal/gamedata directory")
	flag.Parse()

	if err := run(*version, *parent, *srcFlag, *root); err != nil {
		fmt.Fprintln(os.Stderr, "gamedata/gen:", err)
		os.Exit(1)
	}
}

func run(version, parent, srcFlag, root string) error {
	if !isVersionDir(version) {
		return fmt.Errorf("-version must look like v1, v2, ... (got %q)", version)
	}
	src := srcFlag
	if src == "" {
		src = os.Getenv("GAMEDATA_SOURCE")
	}
	if src == "" {
		return fmt.Errorf("no source: set GAMEDATA_SOURCE or pass -source")
	}

	dbRoot := filepath.Join(root, "db")
	assetsDir := filepath.Join(root, "..", "emoji", "assets")

	s, err := loadSource(src)
	if err != nil {
		return err
	}
	res, err := buildDataset(s)
	if err != nil {
		return err
	}
	eff := res.data

	var parentEff *snapshot
	if parent != "" {
		parentEff, err = loadSnapshot(filepath.Join(dbRoot, parent, "snapshot.json"))
		if err != nil {
			return err
		}
		if parentEff == nil {
			return fmt.Errorf("parent %q has no snapshot.json — generate it first", parent)
		}
	}
	prevEff, err := loadSnapshot(filepath.Join(dbRoot, version, "snapshot.json"))
	if err != nil {
		return err
	}

	basis := prevEff
	if basis == nil {
		basis = parentEff
	}
	printReport(res, diffSnapshots(basis, eff), basis, version, parent)

	emitData := eff
	var removed []schema.GDID
	if parent != "" {
		emitData, removed = deltaDataset(*parentEff, eff)
	}
	if err := emitVersion(filepath.Join(dbRoot, version), version, version, parent, emitData, removed); err != nil {
		return err
	}
	if err := writeSnapshot(filepath.Join(dbRoot, version, "snapshot.json"), snapshotOf(version, parent, eff)); err != nil {
		return err
	}
	if err := emitRegistry(root, dbRoot); err != nil {
		return err
	}

	copied, present, err := copyIcons(res.icons, src, assetsDir)
	if err != nil {
		return err
	}
	fmt.Printf("icons:   %d copied, %d already present (union across versions)\n", copied, present)
	fmt.Printf("wrote:   %s\n", filepath.Join(dbRoot, version))
	fmt.Println("done.")
	return nil
}

func printReport(res *buildResult, rep report, basis *snapshot, version, parent string) {
	fmt.Printf("=== gamedata %s", version)
	if parent != "" {
		fmt.Printf(" (delta over %s)", parent)
	}
	fmt.Println(" ===")
	fmt.Printf("items:   %d kept, %d dropped\n", len(res.kept), len(res.dropped))

	reasons := map[string]int{}
	for _, why := range res.dropped {
		reasons[why]++
	}
	for _, k := range sortedKeys(reasons) {
		fmt.Printf("  drop %-22s %d\n", k, reasons[k])
	}
	if len(res.iconMissing) > 0 {
		fmt.Printf("warn:    %d kept items have an icon block but no alias entry (no emoji)\n", len(res.iconMissing))
	}

	if basis == nil {
		fmt.Println("diff:    initial generation (no prior snapshot)")
		return
	}
	fmt.Printf("diff vs %s: items +%d -%d ~%d | contracts +%d -%d ~%d\n",
		basisLabel(basis), len(rep.Items.Added), len(rep.Items.Removed), len(rep.Items.Changed),
		len(rep.Contracts.Added), len(rep.Contracts.Removed), len(rep.Contracts.Changed))
	printIDs("  removed items   ", rep.Items.Removed)
	printIDs("  removed contracts", rep.Contracts.Removed)
	if rep.breaking() {
		fmt.Println("VERDICT: BREAKING — an item/contract was removed. Cut a NEW version (-parent " + version + ") instead of overwriting.")
	} else {
		fmt.Println("VERDICT: backward-compatible — safe to overwrite this version in place.")
	}
}

func basisLabel(s *snapshot) string {
	if s.Version != "" {
		return s.Version
	}
	return "prev"
}

func printIDs(label string, ids []string) {
	if len(ids) == 0 {
		return
	}
	const maxShown = 20
	shown := ids
	suffix := ""
	if len(ids) > maxShown {
		shown = ids[:maxShown]
		suffix = fmt.Sprintf(" … (+%d)", len(ids)-maxShown)
	}
	fmt.Printf("%s: %s%s\n", label, strings.Join(shown, ", "), suffix)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func isVersionDir(name string) bool {
	if len(name) < 2 || name[0] != 'v' {
		return false
	}
	for _, r := range name[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func versionNum(name string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(name, "v"))
	return n
}
