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

// schemaVersion doit correspondre a la version indiquee dans schema.sql.
const schemaVersion = 4

// openDB ouvre (ou cree) le fichier SQLite et applique le schema.
// Le schema est rejouable a chaque demarrage. Une base creee avec
// une ancienne version est refusee avec un message clair, au lieu
// de planter plus tard sur une requete.
func openDB(path string, schemaPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}

	var version, tables int
	_ = db.QueryRow("PRAGMA user_version").Scan(&version)
	_ = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'restaurants'`).Scan(&tables)
	if tables > 0 && version < schemaVersion {
		db.Close()
		return nil, fmt.Errorf(
			"base de donnees ancienne (version %d, attendue %d) : sauvegardez %s puis supprimez-le pour le recreer",
			version, schemaVersion, path)
	}

	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("lecture du schema: %w", err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		db.Close()
		return nil, fmt.Errorf("application du schema: %w", err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		db.Close()
		return nil, fmt.Errorf("version du schema: %w", err)
	}

	return db, nil
}

// seedIfEmpty charge data/restos.json dans la base si elle ne contient encore aucun prix.
func seedIfEmpty(db *sql.DB, jsonPath string) error {
	var count int
	// Utilisation d'un bloc de sécurité au cas où la table "prix" n'est pas encore lue
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

// insertEntry insere ou met a jour un restaurant et un plat,
// puis enregistre le prix correspondant. L'operation est
// rejouable : les memes donnees n'creent jamais de doublon.
func insertEntry(db *sql.DB, e SeedEntry) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Le restaurant est reconnu par son nom et son quartier.
	slug, err := uniqueSlug(tx, e.Resto, e.Quartier)
	if err != nil {
		return fmt.Errorf("adresse courte du restaurant : %w", err)
	}
	var restaurantID string
	err = tx.QueryRow(`
		INSERT INTO restaurants (nom, quartier, type, slug)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (nom, quartier) DO UPDATE SET type = excluded.type
		RETURNING id
	`, e.Resto, e.Quartier, e.Type, slug).Scan(&restaurantID)
	if err != nil {
		return fmt.Errorf("enregistrement du restaurant : %w", err)
	}

	// 2. Le plat est partage entre les restaurants.
	var platID string
	err = tx.QueryRow(`
		INSERT INTO plats (nom)
		VALUES (?)
		ON CONFLICT (nom) DO UPDATE SET nom = excluded.nom
		RETURNING id
	`, e.Plat).Scan(&platID)
	if err != nil {
		return fmt.Errorf("enregistrement du plat : %w", err)
	}

	// 3. Le prix peut etre identique a celui d'autres plats ou
	// d'autres restaurants : aucune unicite sur le montant.
	_, err = tx.Exec(`
		INSERT INTO prix (restaurant_id, plat_id, prix_fcfa, portion)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (restaurant_id, plat_id, portion)
		DO UPDATE SET
			prix_fcfa = excluded.prix_fcfa,
			date_releve = date('now')
	`, restaurantID, platID, e.Prix, e.Portion)
	if err != nil {
		return fmt.Errorf("enregistrement du prix : %w", err)
	}

	return tx.Commit()
}
