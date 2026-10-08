package obsidian

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func entry(title, body string) Entry {
	return Entry{
		Title: title,
		At:    time.Date(2026, 10, 8, 14, 30, 0, 0, time.UTC),
		Body:  body,
	}
}

func TestInsertIntoEmptyDocument(t *testing.T) {
	got := Insert("", "## Réunions", entry("Point budget", "**Résumé** : ok"))

	want := "## Réunions\n\n### 14:30 — Point budget\n\n**Résumé** : ok\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestInsertAppendsHeadingWhenAbsent(t *testing.T) {
	doc := "# 2026-10-08\n\n## Journal\n\nRien à signaler.\n"

	got := Insert(doc, "## Réunions", entry("Daily", "**Résumé** : ok"))

	if !strings.HasPrefix(got, doc[:len(doc)-1]) {
		t.Errorf("existing content was modified:\n%q", got)
	}
	if !strings.Contains(got, "## Réunions\n\n### 14:30 — Daily") {
		t.Errorf("heading not appended correctly:\n%q", got)
	}
}

func TestInsertUnderExistingHeadingBeforeNextSection(t *testing.T) {
	doc := "## Réunions\n\n### 09:00 — Standup\n\nFait.\n\n## Tâches\n\n- [ ] autre chose\n"

	got := Insert(doc, "## Réunions", entry("Point budget", "**Résumé** : ok"))

	iStandup := strings.Index(got, "09:00 — Standup")
	iNew := strings.Index(got, "14:30 — Point budget")
	iTaches := strings.Index(got, "## Tâches")

	if iStandup < 0 || iNew < 0 || iTaches < 0 {
		t.Fatalf("missing content:\n%q", got)
	}
	if !(iStandup < iNew && iNew < iTaches) {
		t.Errorf("new entry not placed at end of its section:\n%q", got)
	}
	if !strings.Contains(got, "- [ ] autre chose") {
		t.Errorf("following section was damaged:\n%q", got)
	}
}

func TestInsertTwiceStacksEntries(t *testing.T) {
	// Idempotence check from the plan: a second append must not corrupt the first.
	doc := Insert("", "## Réunions", entry("Premier", "corps un"))
	doc = Insert(doc, "## Réunions", Entry{
		Title: "Second",
		At:    time.Date(2026, 10, 8, 16, 0, 0, 0, time.UTC),
		Body:  "corps deux",
	})

	if n := strings.Count(doc, "## Réunions"); n != 1 {
		t.Errorf("heading duplicated %d times:\n%q", n, doc)
	}
	iFirst := strings.Index(doc, "14:30 — Premier")
	iSecond := strings.Index(doc, "16:00 — Second")
	if iFirst < 0 || iSecond < 0 || iFirst > iSecond {
		t.Errorf("entries not stacked in order:\n%q", doc)
	}
	if !strings.Contains(doc, "corps un") || !strings.Contains(doc, "corps deux") {
		t.Errorf("a body was lost:\n%q", doc)
	}
	if strings.Contains(doc, "\n\n\n") {
		t.Errorf("blank lines accumulated between appends:\n%q", doc)
	}
}

func TestInsertIgnoresHeadingsInsideCodeFences(t *testing.T) {
	// A fenced block containing "## Tâches" must not be mistaken for the end
	// of the Réunions section.
	doc := "## Réunions\n\n### 09:00 — Standup\n\n```md\n## Tâches\n```\n\nSuite.\n\n## Vrai\n\nx\n"

	got := Insert(doc, "## Réunions", entry("Point", "corps"))

	iFence := strings.Index(got, "```md")
	iNew := strings.Index(got, "14:30 — Point")
	iVrai := strings.Index(got, "## Vrai")
	if iFence < 0 || iNew < 0 || iVrai < 0 {
		t.Fatalf("missing content:\n%q", got)
	}
	if !(iFence < iNew && iNew < iVrai) {
		t.Errorf("entry placed using a heading inside a code fence:\n%q", got)
	}
}

func TestInsertDeeperHeadingsStayInSection(t *testing.T) {
	// "#### Détail" is deeper than "## Réunions" and must not end the section.
	doc := "## Réunions\n\n### 09:00 — Standup\n\n#### Détail\n\nx\n"

	got := Insert(doc, "## Réunions", entry("Point", "corps"))

	if strings.Index(got, "#### Détail") > strings.Index(got, "14:30 — Point") {
		t.Errorf("entry inserted before a deeper heading:\n%q", got)
	}
}

func TestInsertPreservesTrailingSectionExactly(t *testing.T) {
	doc := "## Réunions\n\nx\n\n## Tâches\n\n- [ ] garder\n- [x] fait\n"

	got := Insert(doc, "## Réunions", entry("P", "corps"))

	if !strings.Contains(got, "## Tâches\n\n- [ ] garder\n- [x] fait\n") {
		t.Errorf("trailing section not preserved verbatim:\n%q", got)
	}
}

func TestAppendCreatesFileAndIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Daily", "2026-10-08.md")

	if err := Append(path, "## Réunions", entry("Point", "corps")); err != nil {
		t.Fatalf("Append: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading note: %v", err)
	}
	if !strings.Contains(string(raw), "### 14:30 — Point") {
		t.Errorf("note content:\n%s", raw)
	}

	// The temp file used for the atomic rename must not be left behind.
	ents, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".godio-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestAppendPreservesAccents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "2026-10-08.md")
	body := "**Résumé** : révision du budget, créé par Clément. Déjà validé à 100 %."

	if err := Append(path, "## Réunions", entry("Réunion été", body)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), body) {
		t.Errorf("accents mangled:\n%s", raw)
	}
	if !strings.Contains(string(raw), "Réunion été") {
		t.Errorf("accented title mangled:\n%s", raw)
	}
}

func TestInsertEmptyTitleFallsBack(t *testing.T) {
	got := Insert("", "## Réunions", entry("", "corps"))
	if !strings.Contains(got, "### 14:30 — Réunion") {
		t.Errorf("no fallback title:\n%q", got)
	}
}
