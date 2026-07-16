package storage

import (
	"testing"

	"github.com/BrandonYaniz/yllmd/internal/config"
)

func TestLicenseStorePersistsAndListsAcceptance(t *testing.T) {
	store := NewLicenseStore(config.Config{Paths: config.PathsConfig{StateDir: t.TempDir()}})
	accepted, current, err := store.Accepted("google-gemma", "Gemma Terms", "https://example.com/v1")
	if err != nil || accepted || current != nil {
		t.Fatalf("initial acceptance = %t, %#v, %v", accepted, current, err)
	}
	record, err := store.Accept("google-gemma", "Gemma Terms", "https://example.com/v1", "2026.07.4-draft")
	if err != nil {
		t.Fatal(err)
	}
	accepted, current, err = store.Accepted("google-gemma", "Gemma Terms", "https://example.com/v1")
	if err != nil || !accepted || current == nil || current.AcceptedAt.IsZero() {
		t.Fatalf("stored acceptance = %t, %#v, %v", accepted, current, err)
	}
	if record.FamilyID != "google-gemma" || record.CatalogVersion != "2026.07.4-draft" {
		t.Fatalf("record = %#v", record)
	}
	listed, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].FamilyID != "google-gemma" {
		t.Fatalf("listed = %#v", listed)
	}
}

func TestLicenseStoreRequiresAcceptanceWhenTermsChange(t *testing.T) {
	store := NewLicenseStore(config.Config{Paths: config.PathsConfig{StateDir: t.TempDir()}})
	if _, err := store.Accept("meta-llama", "Llama License", "https://example.com/v1", "v1"); err != nil {
		t.Fatal(err)
	}
	accepted, previous, err := store.Accepted("meta-llama", "Llama License", "https://example.com/v2")
	if err != nil {
		t.Fatal(err)
	}
	if accepted || previous == nil || previous.TermsURL != "https://example.com/v1" {
		t.Fatalf("acceptance = %t, previous = %#v", accepted, previous)
	}
}
