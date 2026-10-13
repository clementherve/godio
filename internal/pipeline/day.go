package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// dayLayout is the date part of a session folder name, which is
// "2006-01-02_150405". Folder names being timestamps is what makes selecting a
// day a prefix match rather than a scan of every session.json.
const dayLayout = "2006-01-02"

// ParseDay turns a --day value into a date.
//
// It accepts an ISO date, "today"/"aujourd'hui", "yesterday"/"hier", and a
// negative number of days ("-2"). now is passed in rather than read, so the
// rule is testable and so a day resolved at 23:59:59 cannot shift under it.
func ParseDay(s string, now time.Time) (time.Time, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	midnight := func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	}

	switch v {
	case "":
		return time.Time{}, fmt.Errorf("date vide")
	case "today", "aujourd'hui", "aujourdhui":
		return midnight(now), nil
	case "yesterday", "hier":
		return midnight(now.AddDate(0, 0, -1)), nil
	}

	if strings.HasPrefix(v, "-") {
		n, err := strconv.Atoi(v)
		if err == nil {
			return midnight(now.AddDate(0, 0, n)), nil
		}
	}

	d, err := time.ParseInLocation(dayLayout, v, now.Location())
	if err != nil {
		return time.Time{}, fmt.Errorf("date %q non comprise (attendu AAAA-MM-JJ, « hier », ou « -2 »)", s)
	}
	return d, nil
}

// OnDay returns every session recorded on a given day, oldest first.
//
// A day can hold several meetings, and replaying a day means replaying all of
// them: the alternative, making the user name one folder, would defeat the
// point of asking by date.
func OnDay(root string, day time.Time) ([]*Session, error) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}

	prefix := day.Format(dayLayout)
	var names []string
	for _, e := range ents {
		if e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("aucune session le %s", prefix)
	}
	sort.Strings(names)

	sessions := make([]*Session, 0, len(names))
	for _, n := range names {
		s, err := OpenSession(filepath.Join(root, n))
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// startedAt reads the recording time back out of a session folder's name.
// Purging and day selection both need the age of a session without opening it,
// and a folder whose name is not a timestamp is not one of ours.
func startedAt(name string) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02_150405", name, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
