// Cheap Dish Map - Saint-Louis
package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
)

var db *sql.DB

// writeJSON écrit une réponse JSON formatée. Les en-têtes CORS globaux sont gérés par le middleware.
func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// writeError renvoie une erreur standardisée au format JSON.
func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"erreur": msg})
}

// GET /health -> Vérification de l'état de la base de données
func handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := db.Ping(); err != nil {
		writeError(w, "base de données inaccessible", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// GET /plats -> Liste de tous les plats distincts triés par ordre alphabétique
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

	if err = rows.Err(); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, plats)
}

// PrixResult définit la structure de retour pour le comparateur de prix
type PrixResult struct {
	Resto      string `json:"resto"`
	Quartier   string `json:"quartier"`
	Type       string `json:"type"`
	Plat       string `json:"plat"`
	PrixFCFA   int    `json:"prix_fcfa"`
	Portion    string `json:"portion"`
	DateReleve string `json:"date_releve"`
}

// GET /plats/{nom}/moins-cher -> Liste les restaurants vendant un plat, du moins cher au plus cher
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

	if err = rows.Err(); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if len(results) == 0 {
		writeError(w, "aucun resto trouvé pour ce plat actuellement à Saint-Louis", http.StatusNotFound)
		return
	}
	writeJSON(w, results)
}

// GET /restos -> Liste complète des établissements
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

	if err = rows.Err(); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, restos)
}

// NewPrixInput définit le schéma JSON attendu pour l'insertion d'un prix
type NewPrixInput struct {
	Resto    string `json:"resto"`
	Quartier string `json:"quartier"`
	Type     string `json:"type"`
	Plat     string `json:"plat"`
	Prix     int    `json:"prix"`
	Portion  string `json:"portion"`
}

// POST /prix -> Ajoute ou met à jour le prix d'un plat (sécurisé par clé d'administration)
func handleAjouterPrix(w http.ResponseWriter, r *http.Request) {
	var in NewPrixInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, "corps JSON invalide", http.StatusBadRequest)
		return
	}
	if in.Resto == "" || in.Plat == "" || in.Prix <= 0 {
		writeError(w, "les champs resto, plat et prix (> 0) sont obligatoires", http.StatusBadRequest)
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
	// Chargement de la clé d'administration depuis l'environnement ou .env
	loadAdminKey()

	var err error
	// Connexion et migration intelligente via votre logique db.go mise à jour
	db, err = openDB("cheapdish.db", "schema.sql")
	if err != nil {
		log.Fatalf("ouverture de la base: %v", err)
	}
	defer db.Close()

	// Chargement du fichier de collecte si la base de données est vide
	if err := seedIfEmpty(db, "data/restos.json"); err != nil {
		log.Fatalf("initialisation des données: %v", err)
	}

	// Configuration du routeur natif (Go 1.22+)
	mux := http.NewServeMux()

	// Routes Publiques
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /plats", handlePlats)
	mux.HandleFunc("GET /plats/{nom}/moins-cher", handleMoinsCher)
	mux.HandleFunc("GET /restos", handleRestos)

	// Écran d'administration Web (Page publique, soumissions sécurisées)
	mux.HandleFunc("GET /admin", handleAdminPage)

	// Routes Sécurisées (Emballées par le middleware de vérification de clé requireAdmin)
	mux.HandleFunc("POST /prix", requireAdmin(handleAjouterPrix))
	mux.HandleFunc("POST /admin/restaurants", requireAdmin(handleAdminAjouterResto))

	port := os.Getenv("PORT")

	if port == "" {
		port = "8080"
	}

	addr := ":" + port

	log.Printf("Serveur Cheap Dish Map démarré sur le port %s", port)

	log.Fatal(http.ListenAndServe(addr, MiddlewareCORS(mux)))
}
