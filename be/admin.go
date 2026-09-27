package main

import (
	"database/sql"
	"embed"
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"
)

//go:embed admin.html
var adminPageFS embed.FS

// adminKey est la clé secrète attendue dans l'en-tête X-Admin-Key.
// À définir dans un fichier .env (voir .env.example) ou en variable
// d'environnement avant de lancer le serveur :
//
//	ADMIN_KEY="un-secret-a-toi" go run .
//
// Sans ça, une valeur par défaut est utilisée UNIQUEMENT pour le développement local.
var adminKey string

func loadAdminKey() {
	// Charge .env s'il existe (ignoré silencieusement s'il est absent —
	// utile en production où la clé vient d'une vraie variable d'environnement).
	_ = godotenv.Load()

	adminKey = os.Getenv("ADMIN_KEY")
	if adminKey == "" {
		adminKey = "changeme"
		log.Println("ATTENTION: ADMIN_KEY non définie, utilisation de la valeur par défaut 'changeme'. Ne jamais faire ça en production.")
	}
}

// requireAdmin protège un handler : la requête doit porter l'en-tête X-Admin-Key
// avec la bonne valeur, sinon elle est rejetée (401).
func requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Admin-Key")
		if key == "" || key != adminKey {
			writeError(w, "accès admin refusé (clé manquante ou invalide)", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// GET /admin -> sert la page HTML avec le formulaire d'ajout de resto.
// La page elle-même est publique, mais chaque soumission doit fournir la bonne clé.
func handleAdminPage(w http.ResponseWriter, r *http.Request) {
	data, err := adminPageFS.ReadFile("admin.html")
	if err != nil {
		writeError(w, "page admin introuvable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

// NewRestaurantInput est le corps JSON attendu par POST /admin/restaurants
type NewRestaurantInput struct {
	Nom      string `json:"nom"`
	Quartier string `json:"quartier"`
	Type     string `json:"type"`
	Contact  string `json:"contact"`
}

// insertRestaurantOnly crée un resto sans plat/prix associé (contrairement à insertEntry).
func insertRestaurantOnly(db *sql.DB, in NewRestaurantInput) (int64, error) {
	_, err := db.Exec(
		`INSERT INTO restaurants (nom, quartier, type, contact) VALUES (?, ?, ?, ?)
		 ON CONFLICT(nom, quartier) DO UPDATE SET type = excluded.type, contact = excluded.contact`,
		in.Nom, in.Quartier, in.Type, in.Contact,
	)
	if err != nil {
		return 0, err
	}
	var id int64
	err = db.QueryRow(`SELECT id FROM restaurants WHERE nom = ? AND quartier = ?`, in.Nom, in.Quartier).Scan(&id)
	return id, err
}

// POST /admin/restaurants -> ajoute un resto (protégé par requireAdmin)
func handleAdminAjouterResto(w http.ResponseWriter, r *http.Request) {
	var in NewRestaurantInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, "corps JSON invalide", http.StatusBadRequest)
		return
	}
	if in.Nom == "" || in.Quartier == "" {
		writeError(w, "nom et quartier sont obligatoires", http.StatusBadRequest)
		return
	}

	id, err := insertRestaurantOnly(db, in)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]interface{}{"status": "resto ajouté", "id": id})
}
