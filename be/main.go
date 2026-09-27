// Cheap Dish Map - Saint-Louis
//
// API qui répond à : "quel resto vend ce plat au prix le moins cher ?"
// Les données vivent maintenant dans une vraie base SQLite (cheapdish.db),
// initialisée à partir du schéma (schema.sql) et peuplée au premier
// démarrage depuis data/restos.json (à remplacer par tes données réelles).
package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

var db *sql.DB

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"erreur": msg})
}

// GET /health
func handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := db.Ping(); err != nil {
		writeError(w, "base de données inaccessible", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// GET /plats -> liste des plats distincts en base
func handlePlats(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`SELECT nom FROM plats ORDER BY nom`)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var plats []string
	for rows.Next() {
		var nom string
		if err := rows.Scan(&nom); err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		plats = append(plats, nom)
	}
	writeJSON(w, plats)
}

// PrixResult est une ligne de résultat pour /plats/{nom}/moins-cher
type PrixResult struct {
	Resto     string `json:"resto"`
	Quartier  string `json:"quartier"`
	Type      string `json:"type"`
	Plat      string `json:"plat"`
	PrixFCFA  int    `json:"prix_fcfa"`
	Portion   string `json:"portion"`
	DateReleve string `json:"date_releve"`
}

// GET /plats/{nom}/moins-cher -> restos triés du moins cher au plus cher
func handleMoinsCher(w http.ResponseWriter, r *http.Request) {
	nom := strings.TrimSpace(r.PathValue("nom"))
	if nom == "" {
		writeError(w, "nom de plat manquant", http.StatusBadRequest)
		return
	}

	rows, err := db.Query(`
		SELECT r.nom, r.quartier, r.type, p.nom, pr.prix_fcfa, pr.portion, pr.date_releve
		FROM prix pr
		JOIN restaurants r ON r.id = pr.restaurant_id
		JOIN plats p ON p.id = pr.plat_id
		WHERE LOWER(p.nom) = LOWER(?)
		ORDER BY pr.prix_fcfa ASC
	`, nom)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var results []PrixResult
	for rows.Next() {
		var res PrixResult
		if err := rows.Scan(&res.Resto, &res.Quartier, &res.Type, &res.Plat, &res.PrixFCFA, &res.Portion, &res.DateReleve); err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		results = append(results, res)
	}

	if len(results) == 0 {
		writeError(w, "aucun resto trouvé pour ce plat", http.StatusNotFound)
		return
	}
	writeJSON(w, results)
}

// GET /restos -> liste des restos
func handleRestos(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`SELECT nom, quartier, type FROM restaurants ORDER BY nom`)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type resto struct {
		Nom      string `json:"nom"`
		Quartier string `json:"quartier"`
		Type     string `json:"type"`
	}
	var restos []resto
	for rows.Next() {
		var rr resto
		if err := rows.Scan(&rr.Nom, &rr.Quartier, &rr.Type); err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		restos = append(restos, rr)
	}
	writeJSON(w, restos)
}

// NewPrixInput est le corps JSON attendu par POST /prix
type NewPrixInput struct {
	Resto    string `json:"resto"`
	Quartier string `json:"quartier"`
	Type     string `json:"type"`
	Plat     string `json:"plat"`
	Prix     int    `json:"prix"`
	Portion  string `json:"portion"`
}

// POST /prix -> ajoute ou met à jour un prix (base pour le crowdsourcing futur)
func handleAjouterPrix(w http.ResponseWriter, r *http.Request) {
	var in NewPrixInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, "corps JSON invalide", http.StatusBadRequest)
		return
	}
	if in.Resto == "" || in.Plat == "" || in.Prix <= 0 {
		writeError(w, "resto, plat et prix (> 0) sont obligatoires", http.StatusBadRequest)
		return
	}

	if err := insertEntry(db, SeedEntry{
		Resto: in.Resto, Quartier: in.Quartier, Type: in.Type,
		Plat: in.Plat, Prix: in.Prix, Portion: in.Portion,
	}); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]string{"status": "enregistré"})
}

func main() {
	loadAdminKey()

	var err error
	db, err = openDB("cheapdish.db", "schema.sql")
	if err != nil {
		log.Fatalf("ouverture de la base: %v", err)
	}
	defer db.Close()

	if err := seedIfEmpty(db, "data/restos.json"); err != nil {
		log.Fatalf("initialisation des données: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /plats", handlePlats)
	mux.HandleFunc("GET /plats/{nom}/moins-cher", handleMoinsCher)
	mux.HandleFunc("GET /restos", handleRestos)
	mux.HandleFunc("POST /prix", requireAdmin(handleAjouterPrix))

	// Espace admin : la page est publique, l'action est protégée par la clé admin.
	mux.HandleFunc("GET /admin", handleAdminPage)
	mux.HandleFunc("POST /admin/restaurants", requireAdmin(handleAdminAjouterResto))

	addr := ":8080"
	log.Printf("serveur démarré sur http://localhost%s (base: cheapdish.db)", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
