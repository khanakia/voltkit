package skillcmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTreeHashDeterministicAndContentSensitive(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, root := range []string{a, b} {
		write(t, root, "SKILL.md", "same content\n")
		write(t, root, "references/x.md", "ref\n")
	}
	ha, err := TreeHash(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, _ := TreeHash(b)
	if ha != hb {
		t.Fatal("identical content must hash identically, regardless of location")
	}
	write(t, b, "references/x.md", "changed\n")
	hb2, _ := TreeHash(b)
	if hb2 == ha {
		t.Fatal("changed content must change the hash")
	}
}

// The .DS_Store scenario that forced the reference-keyed design: junk must
// change NOTHING — not the hash, not the verdict.
func TestTreeHashIgnoresHiddenJunk(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "SKILL.md", "content\n")
	before, _ := TreeHash(dir)
	write(t, dir, ".DS_Store", "macOS junk")
	write(t, dir, "references/.hidden/secret.md", "hidden dir content")
	write(t, dir, "._resource-fork", "more junk")
	after, _ := TreeHash(dir)
	if before != after {
		t.Fatal("hidden junk must not affect the canonical hash")
	}
}

// A renamed file must change the hash even with identical bytes — the path
// is part of the identity (path+NUL+content).
func TestTreeHashPathSensitive(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	write(t, a, "one.md", "x")
	write(t, b, "two.md", "x")
	ha, _ := TreeHash(a)
	hb, _ := TreeHash(b)
	if ha == hb {
		t.Fatal("same bytes under different paths must differ")
	}
}

func installPair(t *testing.T) (ref, installed string) {
	t.Helper()
	ref, installed = t.TempDir(), t.TempDir()
	for _, root := range []string{ref, installed} {
		write(t, root, "SKILL.md", "---\nname: core\n---\nbody\n")
		write(t, root, "references/cmds.md", "commands\n")
	}
	return ref, installed
}

func TestCompareDirsCurrent(t *testing.T) {
	ref, inst := installPair(t)
	res, err := CompareDirs(ref, inst)
	if err != nil || !res.Current || len(res.Stale) != 0 {
		t.Fatalf("identical copies must be current: %+v %v", res, err)
	}
}

func TestCompareDirsModifiedByteIsStale(t *testing.T) {
	ref, inst := installPair(t)
	write(t, inst, "references/cmds.md", "commands!\n") // one byte differs
	res, _ := CompareDirs(ref, inst)
	if res.Current || len(res.Stale) != 1 || res.Stale[0] != "references/cmds.md" {
		t.Fatalf("%+v", res)
	}
}

func TestCompareDirsMissingReferenceFileIsStale(t *testing.T) {
	ref, inst := installPair(t)
	if err := os.Remove(filepath.Join(inst, "references", "cmds.md")); err != nil {
		t.Fatal(err)
	}
	res, _ := CompareDirs(ref, inst)
	if res.Current {
		t.Fatal("a deleted reference file must be stale")
	}
}

// The verdict is keyed on the REFERENCE: installed-side junk and stray user
// files are noted, never stale.
func TestCompareDirsExtrasNeverAffectVerdict(t *testing.T) {
	ref, inst := installPair(t)
	write(t, inst, ".DS_Store", "junk")              // hidden: not even an extra
	write(t, inst, "my-notes.md", "user's own note") // visible extra: noted only
	res, _ := CompareDirs(ref, inst)
	if !res.Current {
		t.Fatalf("extras must never fail the check: %+v", res)
	}
	if len(res.Extras) != 1 || res.Extras[0] != "my-notes.md" {
		t.Fatalf("visible extras noted, hidden junk invisible: %+v", res.Extras)
	}
}

// Both directions of the asymmetry in one test: ref-side junk is also
// excluded (a .DS_Store that sneaks into the repo must not demand one in
// every installed copy).
func TestCompareDirsRefSideJunkExcluded(t *testing.T) {
	ref, inst := installPair(t)
	write(t, ref, ".DS_Store", "repo junk")
	res, _ := CompareDirs(ref, inst)
	if !res.Current {
		t.Fatalf("reference-side dotfiles must not be required: %+v", res)
	}
}
