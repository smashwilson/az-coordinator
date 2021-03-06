package secrets

import (
	"database/sql"
	"fmt"

	"github.com/lib/pq"
	log "github.com/sirupsen/logrus"
)

// Bag contains a loaded set of secrets.
type Bag struct {
	secrets map[string]string
}

// LoadFromDatabase uses a previously initialized DecoderRing to decrypt all secrets currently stored in the database.
// Rows that have been corrupted or that are unparseable once decrypted are skipped and logged.
func LoadFromDatabase(db *sql.DB, ring *DecoderRing) (*Bag, error) {
	var bag Bag
	bag.secrets = make(map[string]string)

	rows, err := db.Query("SELECT key, ciphertext FROM secrets")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var key string
		var ciphertext []byte
		if err := rows.Scan(&key, &ciphertext); err != nil {
			return nil, err
		}

		plaintext, err := ring.Decrypt(ciphertext)
		if err != nil {
			log.WithError(err).Warn("Unable to decrypt ciphertext. Skipping row.")
			continue
		}

		bag.secrets[key] = *plaintext
	}

	return &bag, nil
}

// Len returns the number of known secrets.
func (bag Bag) Len() int {
	return len(bag.secrets)
}

// Set adds a new secret to the bag or overwrites an existing secret with a new value.
func (bag *Bag) Set(key string, value string) {
	bag.secrets[key] = value
}

// Delete removes a key from the secrets bag.
func (bag *Bag) Delete(key string) {
	delete(bag.secrets, key)
}

// Get retrieves an existing secret by key, returning a default value if no secret with this key
// is available.
func (bag Bag) Get(key string, def string) string {
	if value, ok := bag.secrets[key]; ok {
		return value
	}
	return def
}

// GetRequired retrieves an existing secret by key. If no secret with that key is known, an error is
// generated.
func (bag Bag) GetRequired(key string) (string, error) {
	if value, ok := bag.secrets[key]; ok {
		return value, nil
	}
	return "", fmt.Errorf("Missing required secret [%v]", key)
}

// Has returns true if a key corresponds to a known, loaded secret, and false otherwise.
func (bag Bag) Has(key string) bool {
	_, ok := bag.secrets[key]
	return ok
}

// Keys returns a slice containing all known secret keys.
func (bag Bag) Keys() []string {
	ks := make([]string, 0, len(bag.secrets))
	for key := range bag.secrets {
		ks = append(ks, key)
	}
	return ks
}

// SaveToDatabase persists the current state of the bag to an open database connection. Existing secrets
// are truncated, then this bag's contents are encrypted with the provided DecoderRing and written to the
// table in their place.
func (bag Bag) SaveToDatabase(db *sql.DB, ring *DecoderRing, truncate bool) error {
	var ciphertexts = make(map[string][]byte, len(bag.secrets))
	for key, value := range bag.secrets {
		ciphertext, err := ring.Encrypt(value)
		if err != nil {
			log.WithError(err).WithField("key", key).Warn("Unable to encrypt secret.")
			continue
		}
		ciphertexts[key] = ciphertext
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}

	var needsAbort = true
	defer func() {
		if needsAbort {
			if err = tx.Rollback(); err != nil {
				log.WithError(err).Warn("Unable to rollback transaction")
			}
			needsAbort = false
		}
	}()

	if truncate {
		if _, err = tx.Exec("TRUNCATE TABLE secrets"); err != nil {
			return err
		}
	}

	insert, err := tx.Prepare(pq.CopyIn("secrets", "key", "ciphertext"))
	if err != nil {
		return err
	}

	for key, ciphertext := range ciphertexts {
		if _, err = insert.Exec(key, ciphertext); err != nil {
			return err
		}
	}
	if _, err = insert.Exec(); err != nil {
		return err
	}

	err = tx.Commit()
	if err != nil {
		return err
	}
	needsAbort = false

	return nil
}
