package library

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/edit"
)

// UserPiEntry is a declaration in the local user's Pi settings.
type UserPiEntry struct {
	Kind   string `json:"kind"`
	Source string `json:"source"`
	On     bool   `json:"on"`
}

func userPiPath() (string, error) {
	h, err := appdir.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".pi", "agent", "settings.json"), nil
}

func UserPiEntries() ([]UserPiEntry, error) {
	path, err := userPiPath()
	if err != nil {
		return nil, err
	}
	c, err := readPiSettings(path)
	if err != nil {
		return nil, err
	}
	out := []UserPiEntry{}
	for _, kind := range []string{"package", "extension"} {
		for _, raw := range c.entries(kind) {
			if source, ok := piSourceOf(raw); ok {
				out = append(out, UserPiEntry{kind, source, piEntryOn(kind, raw)})
			}
		}
	}
	return out, nil
}

func piEntryOn(kind string, raw json.RawMessage) bool {
	if strings.HasPrefix(piRawOf(raw), `"-`) {
		return false
	}
	if kind == "package" {
		p, ok := piPackageOf(raw)
		return ok && p.On
	}
	return true
}

func switchPiEntry(kind, source string, raw json.RawMessage, on bool) (json.RawMessage, error) {
	if piEntryOn(kind, raw) == on {
		return raw, nil
	}
	if kind == "extension" {
		if on {
			return json.Marshal("+" + source)
		}
		return json.Marshal("-" + source)
	}
	p, _ := piPackageOf(raw)
	if p.Filtered {
		return nil, fmt.Errorf("%s selects package resources; change its filters with pi config", source)
	}
	var entry map[string]json.RawMessage
	if json.Unmarshal(raw, &entry) != nil || entry == nil {
		value, _ := json.Marshal(source)
		entry = map[string]json.RawMessage{"source": value}
	}
	for _, key := range piFilterKeys {
		if on {
			delete(entry, key)
		} else {
			entry[key] = json.RawMessage("[]")
		}
	}
	if len(entry) == 1 {
		return json.Marshal(source)
	}
	return json.Marshal(entry)
}

// ChangeUserPi edits a declaration only. Pi remains responsible for the
// installed package and its files.
func ChangeUserPi(kind, source string, mode PiChange, add bool) error {
	if (kind != "package" && kind != "extension") || strings.TrimSpace(source) == "" {
		return fmt.Errorf("give a Pi package or extension source")
	}
	path, err := userPiPath()
	if err != nil {
		return err
	}
	piMu.Lock()
	defer piMu.Unlock()
	raw, err := edit.Read(path)
	if err != nil {
		return err
	}
	c, err := parsePiSettings(path, raw)
	if err != nil {
		return err
	}
	next := []json.RawMessage{}
	found := false
	changed := false
	for _, entry := range c.entries(kind) {
		if name, ok := piSourceOf(entry); !ok || name != source {
			next = append(next, entry)
			continue
		}
		found = true
		switch {
		case add:
			next = append(next, entry)
		case mode == PiRemove:
			changed = true
		default:
			replacement, err := switchPiEntry(kind, source, entry, mode == PiOn)
			if err != nil {
				return err
			}
			changed = changed || !bytes.Equal(entry, replacement)
			next = append(next, replacement)
		}
	}
	if !found {
		if !add {
			return fmt.Errorf("no user Pi %s %s", kind, source)
		}
		entry, _ := json.Marshal(source)
		next = append(next, entry)
		changed = true
	}
	if !changed {
		return nil
	}
	out, err := edit.PatchJSON(raw, []edit.KV{{Path: kind + "s", Value: next}}, nil)
	if err != nil {
		return err
	}
	current, err := edit.Read(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, current) {
		return fmt.Errorf("%s changed while being edited; retry the command", path)
	}
	return edit.WriteAtomic(path, out)
}
