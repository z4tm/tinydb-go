package main

import (
	"os"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return db, path
}

func TestSetGetReopen(t *testing.T) {
	db, path := openTestDB(t)
	if err := db.Set("name", []byte("zatm")); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := db.Get("name")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "zatm" {
		t.Fatalf("got %q", got)
	}
}

func TestDeleteSurvivesReopen(t *testing.T) {
	db, path := openTestDB(t)
	if err := db.Set("dead", []byte("value")); err != nil {
		t.Fatal(err)
	}
	if err := db.Delete("dead"); err != nil {
		t.Fatal(err)
	}
	if err := db.Set("live", []byte("ok")); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Get("dead"); err == nil {
		t.Fatal("deleted key recovered")
	}
	got, err := db.Get("live")
	if err != nil || string(got) != "ok" {
		t.Fatalf("live key: %q, %v", got, err)
	}
}

func TestPartialTailIsTruncated(t *testing.T) {
	db, path := openTestDB(t)
	if err := db.Set("first", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{1, 2, 3, 4, 5}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("tail not truncated: %d != %d", after.Size(), before.Size())
	}
}

func TestCompact(t *testing.T) {
	db, _ := openTestDB(t)
	defer db.Close()
	if err := db.Set("a", []byte("old")); err != nil {
		t.Fatal(err)
	}
	if err := db.Set("a", []byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := db.Set("b", []byte("gone")); err != nil {
		t.Fatal(err)
	}
	if err := db.Delete("b"); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "compact.db")
	if err := db.Compact(path); err != nil {
		t.Fatal(err)
	}
	compact, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer compact.Close()
	got, err := compact.Get("a")
	if err != nil || string(got) != "new" {
		t.Fatalf("compact value: %q, %v", got, err)
	}
	if _, err := compact.Get("b"); err == nil {
		t.Fatal("deleted key compacted")
	}
}
