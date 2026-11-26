package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxDevices is how many devices the list holds at most.
	MaxDevices = 64
	// MaxName is how many characters a device's name has at most.
	MaxName = 64
)

// Device is a computer of the network that the module can wake.
type Device struct {
	Name string `json:"name"`
	MAC  string `json:"mac"`
}

// CheckDevices checks a list of devices and gives it with each MAC in its one form. A name
// is 1 to 64 printable characters (no control character, no mark that turns the text
// round) with one at least that is not a space; two devices never share a MAC.
func CheckDevices(list []Device) ([]Device, error) {
	if len(list) > MaxDevices {
		return nil, fmt.Errorf("at most %d devices", MaxDevices)
	}
	out := make([]Device, 0, len(list))
	seen := map[string]bool{}
	for i, d := range list {
		if err := checkName(d.Name); err != nil {
			return nil, fmt.Errorf("device %d: %w", i+1, err)
		}
		mac, err := ParseMAC(d.MAC)
		if err != nil {
			return nil, fmt.Errorf("device %d: %w", i+1, err)
		}
		if seen[mac] {
			return nil, fmt.Errorf("device %d: %s is already in the list", i+1, mac)
		}
		seen[mac] = true
		out = append(out, Device{Name: d.Name, MAC: mac})
	}
	return out, nil
}

func checkName(name string) error {
	if !utf8.ValidString(name) {
		return errors.New("the name is not text")
	}
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxName {
		return fmt.Errorf("a name has 1 to %d characters", MaxName)
	}
	for _, r := range name {
		if !unicode.IsPrint(r) {
			return errors.New("a name has printable characters only")
		}
	}
	if strings.TrimSpace(name) == "" {
		return errors.New("a name is not only spaces")
	}
	return nil
}

// file is devices.json.
type file struct {
	Devices []Device `json:"devices"`
}

// Store keeps the list of devices in memory and in a file of its own, which only it writes.
type Store struct {
	path    string
	mu      sync.Mutex
	devices []Device
}

// OpenStore reads the list at path. A missing file is an empty list. A file that cannot be
// read or does not pass CheckDevices is left as it is, said to note, and the list is empty
// until the administrator saves one.
func OpenStore(path string, note func(string)) *Store {
	s := &Store{path: path, devices: []Device{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s
	}
	if err != nil {
		note(fmt.Sprintf("the saved devices cannot be read, the list is empty until it is saved again: %v", err))
		return s
	}
	var f file
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		note(fmt.Sprintf("%s is not valid JSON, the list is empty until it is saved again: %v", path, err))
		return s
	}
	list, err := CheckDevices(f.Devices)
	if err != nil {
		note(fmt.Sprintf("%s is refused, the list is empty until it is saved again: %v", path, err))
		return s
	}
	s.devices = list
	return s
}

// List gives a copy of the devices.
func (s *Store) List() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Device{}, s.devices...)
}

// Has says whether mac, in its one form, is a saved device's.
func (s *Store) Has(mac string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.devices {
		if d.MAC == mac {
			return true
		}
	}
	return false
}

// Replace checks list and saves it in place of the one there: the file is written whole
// under another name, beside it, and renamed onto it, so that it is the old list or the new
// one, never half of one. The list in memory changes only once the file is in place.
func (s *Store) Replace(list []Device) error {
	checked, err := CheckDevices(list)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(file{Devices: checked}, "", "  ")
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".devices-*.json") // 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // (nothing left once it is renamed)
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return err
	}
	s.devices = checked
	return nil
}
