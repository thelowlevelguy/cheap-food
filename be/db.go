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

// openDB ouvre (ou crée) le fichier SQLite et applique le schéma intelligemment.
func openDB(path string, schemaPath string) (*sql.DB, error) {
	// Connexion à SQLite avec l'activation des clés étrangères
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on")
	if err != nil {
		return nil, err
	}

	// ÉVITEMENT DU CRASH: On vérifie si la colonne "actif" existe déjà dans la table restaurants
	var columnExists bool
	checkQuery := "SELECT EXISTS(SELECT 1 FROM pragma_table_info('restaurants') WHERE name='actif')"
	
	// Si la table restaurants n'existe pas encore (première exécution), QueryRow va renvoyer une erreur silencieuse, c'est normal.
	_ = db.QueryRow(checkQuery).Scan(&columnExists)

	// Si la colonne existe déjà, on ne réexécute pas le fichier d'altération pour éviter le crash "duplicate column name"
	if columnExists {
		return db, nil
	}

	// Lecture et application du schéma initial/extensions uniquement si nécessaire
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

// insertEntry insere ou reutilise un restaurant et un plat,
// puis enregistre le prix correspondant.
func insertEntry(db *sql.DB, e SeedEntry) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Rechercher le restaurant existant.
	// Le nom, le quartier et le type servent ici a reconnaitre
	// un restaurant lors de l'importation des donnees.
	var restaurantID int64

	err = tx.QueryRow(`
		SELECT id
		FROM restaurants
		WHERE nom = ? AND quartier = ? AND type = ?
		ORDER BY id
		LIMIT 1
	`, e.Resto, e.Quartier, e.Type).Scan(&restaurantID)

	if err != nil {
		if err != sql.ErrNoRows {
			return fmt.Errorf("recherche du restaurant : %w", err)
		}

		// 2. Si le restaurant n'existe pas, le creer.
		result, err := tx.Exec(`
			INSERT INTO restaurants (nom, quartier, type)
			VALUES (?, ?, ?)
		`, e.Resto, e.Quartier, e.Type)
		if err != nil {
			return fmt.Errorf("creation du restaurant : %w", err)
		}

		restaurantID, err = result.LastInsertId()
		if err != nil {
			return fmt.Errorf("recuperation de l'identifiant du restaurant : %w", err)
		}
	}

	// 3. Rechercher le plat existant.
	var platID int64

	err = tx.QueryRow(`
		SELECT id
		FROM plats
		WHERE nom = ?
	`, e.Plat).Scan(&platID)

	if err != nil {
		if err != sql.ErrNoRows {
			return fmt.Errorf("recherche du plat : %w", err)
		}

		// 4. Creer le plat uniquement s'il n'existe pas.
		result, err := tx.Exec(`
			INSERT INTO plats (nom)
			VALUES (?)
		`, e.Plat)
		if err != nil {
			return fmt.Errorf("creation du plat : %w", err)
		}

		platID, err = result.LastInsertId()
		if err != nil {
			return fmt.Errorf("recuperation de l'identifiant du plat : %w", err)
		}
	}

	// 5. Inserer ou mettre a jour le prix.
	// Le prix peut etre identique a celui d'autres plats
	// ou d'autres restaurants : aucune unicite sur le montant.
	_, err = tx.Exec(`
		INSERT INTO prix (
			restaurant_id,
			plat_id,
			prix_fcfa,
			portion
		)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (restaurant_id, plat_id, portion)
		DO UPDATE SET
			prix_fcfa = excluded.prix_fcfa,
			date_releve = date('now')
	`,
		restaurantID,
		platID,
		e.Prix,
		e.Portion,
	)
	if err != nil {
		return fmt.Errorf("enregistrement du prix : %w", err)
	}

	return tx.Commit()
}