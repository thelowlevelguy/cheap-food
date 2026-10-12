package main

import (
	"database/sql"
	"path/filepath"
	"regexp"
	"testing"
)

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := openDB(filepath.Join(t.TempDir(), "test.db"), "schema.sql")
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func countRows(t *testing.T, d *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := d.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("comptage de %s: %v", table, err)
	}
	return n
}

func TestSchemaCanBeReopened(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	for i := 1; i <= 2; i++ {
		d, err := openDB(path, "schema.sql")
		if err != nil {
			t.Fatalf("ouverture %d: %v", i, err)
		}
		d.Close()
	}
}

func TestOldDatabaseIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec("CREATE TABLE restaurants (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	old.Close()

	d, err := openDB(path, "schema.sql")
	if err == nil {
		d.Close()
		t.Fatal("une ancienne base aurait du etre refusee")
	}
}

func TestInsertRestaurantOnlyReturnsUUIDAndUpserts(t *testing.T) {
	d := newTestDB(t)
	in := NewRestaurantInput{Nom: "Chez Test", Quartier: "Sor", Type: "resto", Contact: "77 000 00 00"}

	id1, err := insertRestaurantOnly(d, in)
	if err != nil {
		t.Fatalf("premier ajout: %v", err)
	}
	if !uuidV4.MatchString(id1) {
		t.Fatalf("identifiant qui n'est pas un UUID v4 : %q", id1)
	}

	in.Contact = "77 111 11 11"
	id2, err := insertRestaurantOnly(d, in)
	if err != nil {
		t.Fatalf("second ajout: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("le meme restaurant doit garder son identifiant : %q puis %q", id1, id2)
	}
	if n := countRows(t, d, "restaurants"); n != 1 {
		t.Fatalf("attendu 1 restaurant, obtenu %d", n)
	}
}

func TestInsertEntryIsIdempotentAndUpdatesPrice(t *testing.T) {
	d := newTestDB(t)
	e := SeedEntry{Resto: "Chez Test", Quartier: "Sor", Type: "cantine", Plat: "Yassa poulet", Prix: 900, Portion: "petite"}

	for i := 0; i < 2; i++ {
		if err := insertEntry(d, e); err != nil {
			t.Fatalf("insertion %d: %v", i+1, err)
		}
	}
	for _, table := range []string{"restaurants", "plats", "prix"} {
		if n := countRows(t, d, table); n != 1 {
			t.Fatalf("%s : attendu 1 ligne, obtenu %d", table, n)
		}
	}

	e.Prix = 1000
	if err := insertEntry(d, e); err != nil {
		t.Fatalf("mise a jour du prix: %v", err)
	}
	var prix int
	if err := d.QueryRow("SELECT prix_fcfa FROM prix").Scan(&prix); err != nil {
		t.Fatal(err)
	}
	if prix != 1000 || countRows(t, d, "prix") != 1 {
		t.Fatalf("le prix aurait du etre mis a jour sans doublon (prix=%d)", prix)
	}
}

func TestSeedFileLoadsOnce(t *testing.T) {
	d := newTestDB(t)
	if err := seedIfEmpty(d, "data/restos.json"); err != nil {
		t.Fatalf("chargement de data/restos.json: %v", err)
	}
	first := countRows(t, d, "prix")
	if first == 0 {
		t.Fatal("aucun prix charge depuis data/restos.json")
	}
	if err := seedIfEmpty(d, "data/restos.json"); err != nil {
		t.Fatal(err)
	}
	if countRows(t, d, "prix") != first {
		t.Fatal("le second chargement ne doit rien ajouter")
	}
}
