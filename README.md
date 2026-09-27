# Cheap Dish Map — Saint-Louis

A small backend that answers one question: **which restaurant sells this dish
at the cheapest price in Saint-Louis, Senegal?**

Built with Go (standard library) + SQLite. Data starts from a field-collected
spreadsheet, gets seeded into a real database, and can be extended by an
admin through a protected web form.

---

## 1. Requirements

- **Go 1.22+** (`go version` to check)
- **A C compiler** (`gcc`), because the SQLite driver (`mattn/go-sqlite3`) uses cgo
  - Linux: `sudo apt install build-essential`
  - macOS: install Xcode Command Line Tools (`xcode-select --install`)

No external database server is needed — SQLite is just a file.

---

## 2. Project structure

```
cheapdish/
├── main.go            # HTTP server + routes
├── db.go              # DB connection, schema loading, seeding logic
├── admin.go           # Admin auth (.env-aware) + "add restaurant" endpoint
├── admin.html          # Admin web form (served at /admin)
├── schema.sql          # Database schema (restaurants, plats, prix)
├── go.mod / go.sum     # Go module + dependencies
├── .env.example         # Template for your local .env (commit this)
├── .env                 # Your real secret — git-ignored, never commit this
├── .gitignore
└── data/
    └── restos.json     # Seed data (replace with your real field data)
```

---

## 3. Setup

```bash
cd cheapdish
go mod download        # downloads dependencies (SQLite driver, dotenv loader)
cp .env.example .env   # then edit .env and set your own ADMIN_KEY
```

`.env` is in `.gitignore` — it will never be committed. `.env.example` is the
template that *does* get committed, with a placeholder value only.

---

## 4. Running the server

```bash
go run .
```

The server reads `ADMIN_KEY` from `.env` automatically. You can also pass it
as a real environment variable instead (useful in production, where you
often won't have a `.env` file at all):
```bash
ADMIN_KEY="choose-a-real-secret" go run .
```

- **Always set a real `ADMIN_KEY`** (via `.env` or the environment). If none
  is found, the server falls back to `changeme` and prints a warning — fine
  for a five-minute local test, never for anything shared or public.
- On first run, the server creates `cheapdish.db` (SQLite file) next to the
  code, applies `schema.sql`, and — **only if the database is still
  empty** — loads `data/restos.json` into it. After that, the database is
  the source of truth; editing `restos.json` again won't change anything
  unless you delete `cheapdish.db` first.

The server listens on `http://localhost:8080`.

To reset everything and reload from `data/restos.json`:
```bash
rm cheapdish.db
ADMIN_KEY="..." go run .
```

---

## 5. Using the API

### Public endpoints (no auth)

| Method | Path                          | What it does                                      |
|--------|-------------------------------|----------------------------------------------------|
| GET    | `/health`                     | Check the server and DB are up                     |
| GET    | `/plats`                      | List all known dish names                          |
| GET    | `/plats/{nom}/moins-cher`     | Restaurants selling `{nom}`, cheapest first         |
| GET    | `/restos`                     | List all restaurants                                |

Examples:
```bash
curl http://localhost:8080/plats

curl "http://localhost:8080/plats/Thiéboudienne/moins-cher"
```

### Admin endpoints (require the admin key)

| Method | Path                     | What it does                          |
|--------|--------------------------|----------------------------------------|
| POST   | `/prix`                  | Add/update a price for a dish          |
| POST   | `/admin/restaurants`     | Add a restaurant (no dish/price yet)   |

Both require the `X-Admin-Key` header with the value you set in `ADMIN_KEY`.
Without it (or with the wrong value), they return `401 Unauthorized`.

**Adding a price:**
```bash
curl -X POST http://localhost:8080/prix \
  -H "Content-Type: application/json" \
  -H "X-Admin-Key: choose-a-real-secret" \
  -d '{"resto":"Chez Fatou","quartier":"Sor","type":"cantine","plat":"Yassa poulet","prix":1200,"portion":"moyenne"}'
```

**Adding a restaurant:**

*Option A — Web form.* Open `http://localhost:8080/admin` in a browser,
fill in the form, and enter the admin key in the "Clé admin" field before
submitting.

*Option B — API directly:*
```bash
curl -X POST http://localhost:8080/admin/restaurants \
  -H "Content-Type: application/json" \
  -H "X-Admin-Key: choose-a-real-secret" \
  -d '{"nom":"Nouveau Resto","quartier":"Sor","type":"resto","contact":"77 000 00 00"}'
```

This adds the restaurant only (no dish/price yet). To also register a price
for it, use `POST /prix` with the same restaurant name and quartier.

---

## 6. Data model

- **restaurants**: `nom`, `quartier`, `type` (`resto` / `cantine` / `rue`), `contact`
- **plats**: dish names (e.g. "Thiéboudienne")
- **prix**: links a restaurant + a dish to a price, a portion size, and the
  date it was recorded

A restaurant is uniquely identified by `(nom, quartier)` — so two
restaurants can share a name if they're in different neighborhoods.

---

## 7. Known limitations (MVP stage)

- Admin auth is a single shared secret key, fine for one admin, not a real
  multi-user login system.
- `/prix` and `/admin/restaurants` are both admin-only for now. When you
  later want regular users to submit prices (crowdsourcing), that will need
  its own, separate, more permissive endpoint with moderation/spam
  protection — don't just reopen `/prix`.
- No frontend yet — this is the API only.
