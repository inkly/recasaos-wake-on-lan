package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckDevices(t *testing.T) {
	got, err := CheckDevices([]Device{{Name: "Desk PC", MAC: "AA-BB-CC-DD-EE-01"}, {Name: "Écran 4K", MAC: "aa:bb:cc:dd:ee:02"}})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].MAC != "aa:bb:cc:dd:ee:01" || got[1].Name != "Écran 4K" {
		t.Fatalf("%v", got)
	}

	sixtyFour := strings.Repeat("é", 64) // 128 bytes, 64 characters
	if _, err := CheckDevices([]Device{{Name: sixtyFour, MAC: "aa:bb:cc:dd:ee:01"}}); err != nil {
		t.Errorf("64 characters refused: %v", err)
	}

	many := make([]Device, 64)
	for i := range many {
		many[i] = Device{Name: fmt.Sprint("pc ", i), MAC: fmt.Sprintf("aa:bb:cc:dd:%02x:%02x", i/256, i%256)}
	}
	if _, err := CheckDevices(many); err != nil {
		t.Errorf("64 devices refused: %v", err)
	}

	for name, list := range map[string][]Device{
		"65 devices":       append(many, Device{Name: "one more", MAC: "aa:bb:cc:dd:ff:ff"}),
		"empty name":       {{Name: "", MAC: "aa:bb:cc:dd:ee:01"}},
		"blank name":       {{Name: "   ", MAC: "aa:bb:cc:dd:ee:01"}},
		"300 characters":   {{Name: strings.Repeat("a", 300), MAC: "aa:bb:cc:dd:ee:01"}},
		"65 characters":    {{Name: sixtyFour + "a", MAC: "aa:bb:cc:dd:ee:01"}},
		"line end":         {{Name: "a\nb", MAC: "aa:bb:cc:dd:ee:01"}},
		"turns text round": {{Name: "a‮b", MAC: "aa:bb:cc:dd:ee:01"}},
		"not UTF-8":        {{Name: "a\xffb", MAC: "aa:bb:cc:dd:ee:01"}},
		"seven bytes":      {{Name: "pc", MAC: "aa:bb:cc:dd:ee:01:02"}},
		"MAC as a script":  {{Name: "pc", MAC: "<script>"}},
		"same MAC twice":   {{Name: "a", MAC: "aa:bb:cc:dd:ee:01"}, {Name: "b", MAC: "AA-BB-CC-DD-EE-01"}},
	} {
		if _, err := CheckDevices(list); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	// a name is text: what looks like markup is the page's to show as text, not the API's to refuse
	if _, err := CheckDevices([]Device{{Name: "<script>alert(1)</script>", MAC: "aa:bb:cc:dd:ee:01"}}); err != nil {
		t.Errorf("a name with < refused: %v", err)
	}
}

func TestStoreSavesAndReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	s := OpenStore(path, func(string) { t.Error("a missing file is not a problem") })
	if len(s.List()) != 0 {
		t.Fatalf("%v, want none", s.List())
	}
	if err := s.Replace([]Device{{Name: "NAS", MAC: "AA-BB-CC-DD-EE-01"}}); err != nil {
		t.Fatal(err)
	}
	if !s.Has("aa:bb:cc:dd:ee:01") || s.Has("aa:bb:cc:dd:ee:02") {
		t.Error("Has is wrong")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("%d files in the folder, want devices.json alone", len(entries))
	}

	again := OpenStore(path, func(note string) { t.Error(note) })
	if l := again.List(); len(l) != 1 || l[0].MAC != "aa:bb:cc:dd:ee:01" || l[0].Name != "NAS" {
		t.Fatalf("read back %v", l)
	}
}

func TestStoreRefusesWhatIsNotValid(t *testing.T) {
	for name, content := range map[string]string{
		"not JSON":     "{",
		"hostile name": `{"devices":[{"name":"a‮b","mac":"aa:bb:cc:dd:ee:01"}]}`,
		"unknown key":  `{"devices":[],"x":1}`,
	} {
		path := filepath.Join(t.TempDir(), "devices.json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		notes := 0
		s := OpenStore(path, func(string) { notes++ })
		if notes != 1 || len(s.List()) != 0 {
			t.Errorf("%s: %d notes and %v, want one note and no device", name, notes, s.List())
		}
		if raw, _ := os.ReadFile(path); string(raw) != content {
			t.Errorf("%s: the file was changed", name)
		}
	}
}

func TestStoreKeepsTheListWhenAWriteFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-folder", "devices.json")
	s := OpenStore(path, func(string) {})
	if err := s.Replace([]Device{{Name: "NAS", MAC: "aa:bb:cc:dd:ee:01"}}); err == nil {
		t.Fatal("no error")
	}
	if len(s.List()) != 0 {
		t.Error("the list changed though nothing was saved")
	}
	if err := s.Replace([]Device{{Name: "", MAC: "aa:bb:cc:dd:ee:01"}}); err == nil {
		t.Error("an invalid list was saved")
	}
}
