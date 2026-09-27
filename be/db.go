package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	_ "github.com/mattn/go-sqlite3"
)

// SeedEntry correspond à une ligne de la feuille de collecte (data/restos.json).
type SeedEntry struct {
	Resto    string `json:"resto"`
	Quartier string `json:"quartier"`
	Type     string `json:"type"`
	Plat     string `json:"plat"`
	Prix     int    `json:"prix"`
	Portion  string `json:"portion"`
}

// openDB ouvre (ou crée) le fichier SQLite et applique le schéma.
func openDB(path string, schemaPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("lecture du schéma: %w", err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		return nil, fmt.Errorf("application du schéma: %w", err)
	}
	return db, nil
}

// seedIfEmpty charge data/restos.json dans la base si elle ne contient encore aucun prix.
// C'est ce que tu remplaceras par tes vraies données collectées sur le terrain.
func seedIfEmpty(db *sql.DB, jsonPath string) error {
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM prix").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil // déjà peuplée, on ne touche à rien
	}

	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		return fmt.Errorf("lecture de %s: %w", jsonPath, err)
	}
	var entries []SeedEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return err
	}

	for _, e := range entries {
		if err := insertEntry(db, e); err != nil {
			return fmt.Errorf("insertion de %+v: %w", e, err)
		}
	}
	return nil
}

// insertEntry insère (ou réutilise) un resto et un plat, puis enregistre le prix.
func insertEntry(db *sql.DB, e SeedEntry) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT INTO restaurants (nom, quartier, type) VALUES (?, ?, ?)
		 ON CONFLICT(nom, quartier) DO UPDATE SET type = excluded.type`,
		e.Resto, e.Quartier, e.Type,
	); err != nil {
		return err
	}
	var restaurantID int64
	if err := tx.QueryRow(`SELECT id FROM restaurants WHERE nom = ? AND quartier = ?`, e.Resto, e.Quartier).Scan(&restaurantID); err != nil {
		return err
	}

	if _, err := tx.Exec(`INSERT INTO plats (nom) VALUES (?) ON CONFLICT(nom) DO NOTHING`, e.Plat); err != nil {
		return err
	}
	var platID int64
	if err := tx.QueryRow(`SELECT id FROM plats WHERE nom = ?`, e.Plat).Scan(&platID); err != nil {
		return err
	}

	if _, err := tx.Exec(
		`INSERT INTO prix (restaurant_id, plat_id, prix_fcfa, portion) VALUES (?, ?, ?, ?)
		 ON CONFLICT(restaurant_id, plat_id, portion) DO UPDATE SET prix_fcfa = excluded.prix_fcfa, date_releve = date('now')`,
		restaurantID, platID, e.Prix, e.Portion,
	); err != nil {
		return err
	}

	return tx.Commit()
}
