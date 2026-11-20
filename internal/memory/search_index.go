package memory

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/luist18/sqlite-vec-go"
	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

const (
	searchSchemaVersion  = 1
	searchPrivacyVersion = 1
)

const (
	SearchBackendSQLite = "sqlite"
	SearchBackendHybrid = "hybrid"
)

var errIndexGeneration = errors.New("search index generation changed")

type searchIndex struct {
	db       *sql.DB
	dir      string
	embedder EmbeddingProvider
}

func openSearchIndex(projectDir string, embedder EmbeddingProvider) (*searchIndex, error) {
	dir := filepath.Join(projectDir, ".engine", "search")
	if err := createPrivateDir(dir); err != nil {
		return nil, err
	}
	if err := validatePrivateDir(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "index-v1.db")
	db, err := driver.Open("file:"+path, func(c *sqlite3.Conn) error {
		if err := fts5.Register(c); err != nil {
			return err
		}
		return c.BusyTimeout(2 * time.Second)
	})
	if err != nil {
		return nil, err
	}
	idx := &searchIndex{db: db, dir: dir, embedder: embedder}
	if err := idx.init(); err != nil {
		db.Close()
		return nil, err
	}
	return idx, nil
}

func (idx *searchIndex) init() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS search_meta (key TEXT PRIMARY KEY, value INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS search_docs (
			rel TEXT PRIMARY KEY, hash TEXT NOT NULL,
			name TEXT NOT NULL, description TEXT NOT NULL, body TEXT NOT NULL, scene TEXT NOT NULL)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS search_fts USING fts5(rel UNINDEXED, tokens)`,
	}
	for _, statement := range statements {
		if _, err := idx.db.Exec(statement); err != nil {
			return err
		}
	}
	for _, version := range []struct {
		key   string
		value int
	}{{"schema", searchSchemaVersion}, {"privacy", searchPrivacyVersion}} {
		var current int
		err := idx.db.QueryRow(`SELECT value FROM search_meta WHERE key = ?`, version.key).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err := idx.db.Exec(`INSERT INTO search_meta(key, value) VALUES (?, ?)`, version.key, version.value); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if current != version.value {
			return fmt.Errorf("unsupported search index %s %d", version.key, current)
		}
	}
	if _, err := idx.db.Exec(`INSERT OR IGNORE INTO search_meta(key, value) VALUES ('generation', 0)`); err != nil {
		return err
	}
	if idx.embedder == nil {
		return nil
	}
	vectorStatements := []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS search_vec USING vec0(embedding float[` +
			strconv.Itoa(idx.embedder.Dimensions()) + `] distance_metric=cosine)`,
		`CREATE TABLE IF NOT EXISTS search_vec_meta (
			rowid INTEGER PRIMARY KEY, rel TEXT UNIQUE NOT NULL,
			hash TEXT NOT NULL, scene TEXT NOT NULL,
			provider TEXT NOT NULL, model TEXT NOT NULL)`,
	}
	for _, statement := range vectorStatements {
		if _, err := idx.db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func (idx *searchIndex) generation() (int64, error) {
	var value int64
	err := idx.db.QueryRow(`SELECT value FROM search_meta WHERE key = 'generation'`).Scan(&value)
	return value, err
}

func vectorLiteral(vector []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, value := range vector {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%.6f", value)
	}
	b.WriteByte(']')
	return b.String()
}

func (idx *searchIndex) embeddingUpdates(corpus []searchFact, stored map[string]string) (map[string]string, error) {
	updates := make(map[string]string)
	if idx.embedder == nil {
		return updates, nil
	}
	embedded := make(map[string]string)
	rows, err := idx.db.Query(`SELECT rel, hash FROM search_vec_meta`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var rel, hash string
		if err := rows.Scan(&rel, &hash); err != nil {
			rows.Close()
			return nil, err
		}
		embedded[rel] = hash
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	var changed []searchFact
	for _, fact := range corpus {
		if stored[fact.RelPath] != fact.SourceHash || embedded[fact.RelPath] != fact.SourceHash {
			changed = append(changed, fact)
		}
	}
	if len(changed) == 0 {
		return updates, nil
	}
	texts := make([]string, len(changed))
	for i, fact := range changed {
		texts[i] = fact.Name + ". " + fact.Description + ". " + fact.Body
	}
	for start := 0; start < len(texts); start += 32 {
		end := start + 32
		if end > len(texts) {
			end = len(texts)
		}
		vectors, err := idx.embedder.Embed(context.Background(), texts[start:end])
		if err != nil {
			return nil, err
		}
		for i, vector := range vectors {
			updates[changed[start+i].RelPath] = vectorLiteral(vector)
		}
	}
	return updates, nil
}

func (idx *searchIndex) reconcile(corpus []searchFact) (int64, error) {
	for attempt := 0; attempt < 2; attempt++ {
		start, err := idx.generation()
		if err != nil {
			return 0, err
		}
		rows, err := idx.db.Query(`SELECT rel, hash FROM search_docs`)
		if err != nil {
			return 0, err
		}
		stored := make(map[string]string)
		for rows.Next() {
			var rel, hash string
			if err := rows.Scan(&rel, &hash); err != nil {
				rows.Close()
				return 0, err
			}
			stored[rel] = hash
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return 0, err
		}
		rows.Close()

		vectors, err := idx.embeddingUpdates(corpus, stored)
		if err != nil {
			indexDiagnostic(err)
		}
		current := make(map[string]bool, len(corpus))
		dirty := len(stored) != len(corpus) || len(vectors) != 0
		for _, fact := range corpus {
			current[fact.RelPath] = true
			dirty = dirty || stored[fact.RelPath] != fact.SourceHash
		}
		if !dirty {
			return start, nil
		}

		tx, err := idx.db.Begin()
		if err != nil {
			return 0, err
		}
		for _, fact := range corpus {
			vector, hasVector := vectors[fact.RelPath]
			if stored[fact.RelPath] == fact.SourceHash && !hasVector {
				continue
			}
			if stored[fact.RelPath] != fact.SourceHash {
				if _, err := tx.Exec(`INSERT INTO search_docs(rel, hash, name, description, body, scene)
					VALUES (?, ?, ?, ?, ?, ?)
					ON CONFLICT(rel) DO UPDATE SET hash=excluded.hash, name=excluded.name,
					description=excluded.description, body=excluded.body, scene=excluded.scene`,
					fact.RelPath, fact.SourceHash, fact.Name, fact.Description, fact.Body, fact.Scene); err != nil {
					tx.Rollback()
					return 0, err
				}
				if _, exists := stored[fact.RelPath]; exists {
					if _, err := tx.Exec(`DELETE FROM search_fts WHERE rel = ?`, fact.RelPath); err != nil {
						tx.Rollback()
						return 0, err
					}
				}
				if _, err := tx.Exec(`INSERT INTO search_fts(rel, tokens) VALUES (?, ?)`,
					fact.RelPath, indexTokens(fact)); err != nil {
					tx.Rollback()
					return 0, err
				}
			}
			if !hasVector {
				continue
			}
			var rowID int64
			err = tx.QueryRow(`SELECT rowid FROM search_vec_meta WHERE rel = ?`, fact.RelPath).Scan(&rowID)
			if errors.Is(err, sql.ErrNoRows) {
				if err := tx.QueryRow(`SELECT COALESCE(MAX(rowid), 0) + 1 FROM search_vec_meta`).Scan(&rowID); err != nil {
					tx.Rollback()
					return 0, err
				}
			} else if err != nil {
				tx.Rollback()
				return 0, err
			}
			if _, err := tx.Exec(`DELETE FROM search_vec WHERE rowid = ?`, rowID); err != nil {
				tx.Rollback()
				return 0, err
			}
			if _, err := tx.Exec(`INSERT INTO search_vec(rowid, embedding) VALUES (?, ?)`, rowID, vector); err != nil {
				tx.Rollback()
				return 0, err
			}
			if _, err := tx.Exec(`INSERT INTO search_vec_meta(rowid, rel, hash, scene, provider, model)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(rel) DO UPDATE SET hash=excluded.hash, scene=excluded.scene,
				provider=excluded.provider, model=excluded.model`,
				rowID, fact.RelPath, fact.SourceHash, fact.Scene,
				idx.embedder.Name(), idx.embedder.Name()); err != nil {
				tx.Rollback()
				return 0, err
			}
		}
		for rel := range stored {
			if current[rel] {
				continue
			}
			if _, err := tx.Exec(`DELETE FROM search_docs WHERE rel = ?`, rel); err != nil {
				tx.Rollback()
				return 0, err
			}
			if _, err := tx.Exec(`DELETE FROM search_fts WHERE rel = ?`, rel); err != nil {
				tx.Rollback()
				return 0, err
			}
			if idx.embedder != nil {
				var rowID int64
				err := tx.QueryRow(`SELECT rowid FROM search_vec_meta WHERE rel = ?`, rel).Scan(&rowID)
				if err == nil {
					if _, err := tx.Exec(`DELETE FROM search_vec WHERE rowid = ?`, rowID); err != nil {
						tx.Rollback()
						return 0, err
					}
					if _, err := tx.Exec(`DELETE FROM search_vec_meta WHERE rowid = ?`, rowID); err != nil {
						tx.Rollback()
						return 0, err
					}
				} else if !errors.Is(err, sql.ErrNoRows) {
					tx.Rollback()
					return 0, err
				}
			}
		}
		result, err := tx.Exec(`UPDATE search_meta SET value = value + 1 WHERE key = 'generation' AND value = ?`, start)
		if err != nil {
			tx.Rollback()
			return 0, err
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			tx.Rollback()
			if err != nil {
				return 0, err
			}
			if attempt == 0 {
				continue
			}
			return 0, errIndexGeneration
		}
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		return start + 1, nil
	}
	return 0, errIndexGeneration
}

func indexDiagnostic(err error) {
	fmt.Fprintf(os.Stderr, "recollect: search index unavailable: %v\n", err)
}

func (s *Store) ensureSearchIndex() (*searchIndex, error) {
	s.indexMu.Lock()
	defer s.indexMu.Unlock()
	if s.searchIndex != nil {
		return s.searchIndex, nil
	}
	idx, err := openSearchIndex(s.projectDir, s.embedder)
	if err != nil {
		return nil, err
	}
	s.searchIndex = idx
	return idx, nil
}

// Close releases the derived search index handle. Markdown stays
// authoritative, so a store without an index needs no cleanup.
func (s *Store) Close() error {
	s.indexMu.Lock()
	defer s.indexMu.Unlock()
	if s.searchIndex == nil {
		return nil
	}
	err := s.searchIndex.db.Close()
	s.searchIndex = nil
	return err
}

func encodeToken(token string) string {
	return "t" + hex.EncodeToString([]byte(token))
}

func indexTokens(fact searchFact) string {
	terms := make([]string, 0, 16)
	for _, field := range []string{fact.Name, fact.Description, fact.Body} {
		for _, token := range tokenize(field) {
			terms = append(terms, encodeToken(token))
		}
	}
	return strings.Join(terms, " ")
}

func (idx *searchIndex) ftsCandidates(query string) (map[string]bool, error) {
	tokens := queryTokens(query)
	rels := make(map[string]bool)
	if len(tokens) == 0 {
		return rels, nil
	}
	terms := make([]string, 0, len(tokens))
	for _, token := range tokens {
		terms = append(terms, `"`+encodeToken(token)+`"`)
	}
	rows, err := idx.db.Query(`SELECT rel FROM search_fts WHERE search_fts MATCH ?`, strings.Join(terms, " OR "))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var rel string
		if err := rows.Scan(&rel); err != nil {
			return nil, err
		}
		rels[rel] = true
	}
	return rels, rows.Err()
}

func (idx *searchIndex) semanticCandidates(query, scene string) (map[string]float64, error) {
	if idx.embedder == nil {
		return nil, errNoEmbedder
	}
	vectors, err := idx.embedder.Embed(context.Background(), []string{query})
	if err != nil || len(vectors) == 0 {
		if err == nil {
			err = errors.New("embedding provider returned no vector")
		}
		return nil, err
	}
	rows, err := idx.db.Query(`SELECT m.rel, v.distance FROM search_vec v
		JOIN search_vec_meta m ON m.rowid = v.rowid
		JOIN search_docs d ON d.rel = m.rel
		WHERE v.embedding MATCH ? AND v.k = ? AND m.hash = d.hash AND (m.scene = '' OR m.scene = ?)
		ORDER BY v.distance`, vectorLiteral(vectors[0]), 20, scene)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]float64)
	for rows.Next() {
		var rel string
		var distance float64
		if err := rows.Scan(&rel, &distance); err != nil {
			return nil, err
		}
		out[rel] = distance
	}
	return out, rows.Err()
}

func (s *Store) RefreshSearch() error {
	if s.searchBackend == "" {
		return nil
	}
	corpus, err := s.searchCorpus()
	if err != nil {
		return err
	}
	idx, err := s.ensureSearchIndex()
	if err != nil {
		return err
	}
	_, err = idx.reconcile(corpus)
	return err
}

func (s *Store) semanticCandidates(query, scene string) (map[string]float64, error) {
	idx, err := s.ensureSearchIndex()
	if err != nil {
		return nil, err
	}
	return idx.semanticCandidates(query, scene)
}

func (s *Store) indexedSearch(corpus []searchFact, query string) map[string]bool {
	idx, err := s.ensureSearchIndex()
	if err != nil {
		indexDiagnostic(err)
		return nil
	}
	generation, err := idx.reconcile(corpus)
	if err != nil {
		indexDiagnostic(err)
		return nil
	}
	rels, err := idx.ftsCandidates(query)
	if err != nil {
		indexDiagnostic(err)
		return nil
	}
	// Another process can publish an index for a different corpus while FTS runs.
	current, err := idx.generation()
	if err != nil {
		indexDiagnostic(err)
		return nil
	}
	if current != generation {
		return nil
	}
	normalized := strings.TrimSpace(normalizeText(query))
	phraseHit := func(text string) bool { return strings.Contains(normalizeText(text), normalized) }
	for _, fact := range corpus {
		if rels[fact.RelPath] {
			continue
		}
		if phraseHit(fact.Name) || phraseHit(fact.Description) || phraseHit(fact.Body) {
			rels[fact.RelPath] = true
		}
	}
	return rels
}
