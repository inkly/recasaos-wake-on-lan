package main

import (
	"encoding/json"
	"os"
	"testing"
	"unicode/utf8"
)

// The catalogue of ReCasaOS refuses a module whose description is longer than 300 characters (docs/modules.md of CasaOS-Install).
func TestManifestDescriptionFitsTheCatalogue(t *testing.T) {
	raw, err := os.ReadFile("module.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if n := utf8.RuneCountInString(manifest.Description); n > 300 {
		t.Fatalf("description has %d characters, the catalogue takes at most 300", n)
	}
}
