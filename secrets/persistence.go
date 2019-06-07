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
	FilenameTLSCertificate = "/etc/ssl/pushbot.party/fullchain.pem"
	FilenameTLSKey         = "/etc/ssl/pushbot.party/privkey.pem"
	FilenameDHParams       = "/etc/ssl/dhparams.pem"
)

var TLSKeysToPath = map[string]string{
	"TLS_CERTIFICATE": FilenameTLSCertificate,
	"TLS_KEY":         FilenameTLSKey,
	"TLS_DH_PARAMS":   FilenameDHParams,
}

type SecretsBag struct {
	secrets map[string]string
}

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

func (bag *SecretsBag) Len() int {
	return len(bag.secrets)
}

func (bag *SecretsBag) Set(key string, value string) {
	bag.secrets[key] = value
}

func (bag SecretsBag) Get(key string, def string) string {
	if value, ok := bag.secrets[key]; ok {
		return value
	} else {
		return def
	}
}

func (bag SecretsBag) GetRequired(key string) (string, error) {
	if value, ok := bag.secrets[key]; ok {
		return value, nil
	} else {
		return "", fmt.Errorf("Missing required secret [%v]", key)
	}
}

func (bag SecretsBag) DesiredTLSFiles() (map[string][]byte, error) {
	desiredContents := make(map[string][]byte, len(TLSKeysToPath))
	for key, path := range TLSKeysToPath {
		desired, err := bag.GetRequired(key)
		if err != nil {
			return nil, err
		}
		desiredContents[path] = []byte(desired)
	}
	return desiredContents, nil
}

func (bag SecretsBag) ActualTLSFiles() (map[string][]byte, error) {
	actualContents := make(map[string][]byte, len(TLSKeysToPath))
	for _, path := range TLSKeysToPath {
		actual, err := ioutil.ReadFile(path)
		if err == nil {
			actualContents[path] = actual
		} else if err == os.ErrNotExist {
			actualContents[path] = nil
		} else {
			return nil, err
		}
	}
	return actualContents, nil
}

func (bag SecretsBag) SaveToDatabase(db *sql.DB, ring *DecoderRing) error {
	var ciphertexts = make([][]byte, len(bag.secrets))
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
