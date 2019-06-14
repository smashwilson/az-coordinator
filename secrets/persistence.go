package secrets

import (
	"database/sql"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"

	"github.com/lib/pq"
	log "github.com/sirupsen/logrus"
)

const (
	// FilenameTLSCertificate is the path to the file containing the full chain of public TLS certificates.
	FilenameTLSCertificate = "/etc/ssl/az/pushbot.party/fullchain.pem"

	// FilenameTLSKey is the path to the file containing the TLS private key.
	FilenameTLSKey = "/etc/ssl/az/pushbot.party/privkey.pem"

	// FilenameDHParams is the path to a file containing pre-generated DH parameters.
	FilenameDHParams = "/etc/ssl/az/dhparams.pem"
)

var tlsKeysToPath = map[string]string{
	"TLS_CERTIFICATE": FilenameTLSCertificate,
	"TLS_KEY":         FilenameTLSKey,
	"TLS_DH_PARAMS":   FilenameDHParams,
}

type SecretsBag struct {
	secrets map[string]string
}

// LoadFromDatabase uses a previously initialized DecoderRing to decrypt all secrets currently stored in the database.
// Rows that have been corrupted or that are unparseable once decrypted are skipped and logged.
func LoadFromDatabase(db *sql.DB, ring *DecoderRing) (*SecretsBag, error) {
	var bag SecretsBag
	bag.secrets = make(map[string]string)

	keyRx := regexp.MustCompile(`\A([^=]+)=(.*)\z`)

	rows, err := db.Query("SELECT ciphertext FROM secrets")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var ciphertext []byte
		if err := rows.Scan(&ciphertext); err != nil {
			return nil, err
		}

		plaintext, err := ring.Decrypt(ciphertext)
		if err != nil {
			log.WithError(err).Warn("Unable to decrypt ciphertext. Skipping row.")
			continue
		}

		matches := keyRx.FindStringSubmatch(*plaintext)
		if matches == nil {
			log.Warn("Unable to parse secret value.")
			continue
		}

		bag.secrets[matches[1]] = matches[2]
	}

	return &bag, nil
}

// Len returns the number of known secrets.
func (bag *SecretsBag) Len() int {
	return len(bag.secrets)
}

// Set adds a new secret to the bag or overwrites an existing secret with a new value.
func (bag *SecretsBag) Set(key string, value string) {
	bag.secrets[key] = value
}

// Get retrieves an existing secret by key, returning a default value if no secret with this key
// is available.
func (bag SecretsBag) Get(key string, def string) string {
	if value, ok := bag.secrets[key]; ok {
		return value
	}
	return def
}

// GetRequired retrieves an existing secret by key. If no secret with that key is known, an error is
// generated.
func (bag SecretsBag) GetRequired(key string) (string, error) {
	if value, ok := bag.secrets[key]; ok {
		return value, nil
	}
	return "", fmt.Errorf("Missing required secret [%v]", key)
}

// DesiredTLSFiles constructs a map whose keys are paths on the filesystem and whose values are the contents
// of TLS-related files that are expected to be placed at those paths. An error is returned if any of the
// required TLS secret keys are absent.
func (bag SecretsBag) DesiredTLSFiles() (map[string][]byte, error) {
	desiredContents := make(map[string][]byte, len(tlsKeysToPath))
	for key, path := range tlsKeysToPath {
		desired, err := bag.GetRequired(key)
		if err != nil {
			return nil, err
		}
		desiredContents[path] = []byte(desired)
	}
	return desiredContents, nil
}

// ActualTLSFiles constructs a map whose keys are paths on the filesystem and whose values are the actual
// contents of files at those locations on disk. Any file not yet present has a value of nil.
func (bag SecretsBag) ActualTLSFiles() (map[string][]byte, error) {
	actualContents := make(map[string][]byte, len(tlsKeysToPath))
	for _, path := range tlsKeysToPath {
		actual, err := ioutil.ReadFile(path)
		if err == nil {
			actualContents[path] = actual
		} else if os.IsNotExist(err) {
			actualContents[path] = nil
		} else {
			return nil, err
		}
	}
	return actualContents, nil
}

// SaveToDatabase persists the current state of the bag to an open database connection. Existing secrets
// are truncated, then this bag's contents are encrypted with the provided DecoderRing and written to the
// table in their place.
func (bag SecretsBag) SaveToDatabase(db *sql.DB, ring *DecoderRing) error {
	var ciphertexts = make([][]byte, 0, len(bag.secrets))
	for key, value := range bag.secrets {
		plaintext := key + "=" + value
		ciphertext, err := ring.Encrypt(plaintext)
		if err != nil {
			log.WithError(err).WithField("key", key).Warn("Unable to encrypt secret.")
			continue
		}
		ciphertexts = append(ciphertexts, ciphertext)
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

	if _, err = tx.Exec("TRUNCATE TABLE secrets"); err != nil {
		return err
	}

	insert, err := tx.Prepare(pq.CopyIn("secrets", "ciphertext"))
	if err != nil {
		return err
	}

	for _, ciphertext := range ciphertexts {
		if _, err = insert.Exec(ciphertext); err != nil {
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
