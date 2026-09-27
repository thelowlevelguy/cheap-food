-- Schéma de la base de données Cheap Dish Map (Saint-Louis)

CREATE TABLE IF NOT EXISTS restaurants (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    nom       TEXT NOT NULL,
    quartier  TEXT NOT NULL,
    type      TEXT NOT NULL,       -- resto | cantine | rue
    contact   TEXT,                -- téléphone, optionnel
    UNIQUE(nom, quartier)
);

CREATE TABLE IF NOT EXISTS plats (
    id    INTEGER PRIMARY KEY AUTOINCREMENT,
    nom   TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS prix (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    restaurant_id INTEGER NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
    plat_id       INTEGER NOT NULL REFERENCES plats(id) ON DELETE CASCADE,
    prix_fcfa     INTEGER NOT NULL,
    portion       TEXT,                    -- petite | moyenne | grande
    date_releve   TEXT NOT NULL DEFAULT (date('now')),
    UNIQUE(restaurant_id, plat_id, portion)
);

CREATE INDEX IF NOT EXISTS idx_prix_plat ON prix(plat_id);
