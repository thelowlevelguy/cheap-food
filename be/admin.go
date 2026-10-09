package main

import (
	"crypto/subtle"
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
var adminKey string

func loadAdminKey() {
    _ = godotenv.Load()

    adminKey = os.Getenv("ADMIN_KEY")
    if adminKey == "" {
        log.Fatal("ERREUR: la variable ADMIN_KEY doit être définie avant le démarrage du serveur.")
    }
}

// MiddlewareCORS configure les en-têtes requis pour autoriser votre PWA Angular (GitHub Pages).
func MiddlewareCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		allowedOrigins := map[string]bool{
			"http://localhost:4200":             true,
			"http://127.0.0.1:4200":             true,
			"https://thelowlevelguy.github.io": true,
		}

		if allowedOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set(
				"Access-Control-Allow-Methods",
				"GET, POST, PUT, PATCH, DELETE, OPTIONS",
			)
			w.Header().Set(
				"Access-Control-Allow-Headers",
				"Content-Type, X-Admin-Key, Authorization",
			)
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// requireAdmin protège un handler : vérification sécurisée par comparaison à temps constant.
func requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Admin-Key")
		
		// Utilisation de ConstantTimeCompare pour éviter les fuites d'informations temporelles (Timing Attacks)
		if key == "" || subtle.ConstantTimeCompare([]byte(key), []byte(adminKey)) != 1 {
			writeError(w, "accès admin refusé (clé manquante ou invalide)", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// GET /admin -> sert la page HTML avec le formulaire d'ajout de resto.
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

// insertRestaurantOnly crée un resto sans plat/prix associé.
// Prise en charge de la structure UUID de votre base de données étendue.
func insertRestaurantOnly(db *sql.DB, in NewRestaurantInput) (string, error) {
	// Génération d'un UUID en minuscule natif à SQLite lors de l'insertion
	query := `
		INSERT INTO restaurants (id, nom, quartier, type, contact, actif) 
		VALUES (lower(hex(randomblob(16))), ?, ?, ?, ?, 1)
		ON CONFLICT(nom, quartier) DO UPDATE SET 
			type = excluded.type, 
			contact = excluded.contact,
			actif = 1`
			
	_, err := db.Exec(query, in.Nom, in.Quartier, in.Type, in.Contact)
	if err != nil {
		return "", err
	}

	var id string
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
