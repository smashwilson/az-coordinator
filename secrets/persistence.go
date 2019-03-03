package secrets

import (
	"database/sql"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"regexp"

	"github.com/lib/pq"
)

const (
	FilenameTLSCertificate = "/etc/ssl/pushbot.party/fullchain.pem"
	FilenameTLSKey         = "/etc/ssl/pushbot.party/privkey.pem"
	FilenameDHParams       = "/etc/ssl/dhparams.pem"
)

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
		if err := rows.Scan(ciphertext); err != nil {
			log.Printf("Unable to read secrets result row: %v.\n", err)
			continue
		}

		plaintext, err := ring.Decrypt(ciphertext)
		if err != nil {
			return nil, err
		}

		matches := keyRx.FindStringSubmatch(*plaintext)
		if matches == nil {
			log.Println("Unable to parse secret value")
			continue
		}

		bag.secrets[matches[1]] = matches[2]
	}

	return &bag, nil
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

func (bag SecretsBag) GetRequired(key string) (*string, error) {
	if value, ok := bag.secrets[key]; ok {
		return &value, nil
	} else {
		return nil, fmt.Errorf("Missing required secret [%v]", key)
	}
}

func (bag SecretsBag) WriteTLSFiles() (bool, error) {
	var changed = false

	ch, err := bag.writeToFile("TLS_CERTIFICATE", FilenameTLSCertificate)
	changed = changed || ch
	if err != nil {
		return changed, err
	}

	ch, err = bag.writeToFile("TLS_KEY", FilenameTLSKey)
	changed = changed || ch
	if err != nil {
		return changed, err
	}

	ch, err = bag.writeToFile("TLS_DH_PARAMS", FilenameDHParams)
	changed = changed || ch
	if err != nil {
		return changed, err
	}

	return changed, nil
}

func (bag SecretsBag) SaveToDatabase(db *sql.DB, ring *DecoderRing) error {
	var ciphertexts = make([][]byte, len(bag.secrets))
	for key, value := range bag.secrets {
		plaintext := key + "=" + value
		ciphertext, err := ring.Encrypt(plaintext)
		if err != nil {
			log.Printf("Unable to encrypt secret %v: %v. Skipping.\n", key, err)
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
				log.Printf("Unable to rollback transaction: %v\n", err)
			}
			needsAbort = false
		}
	}()

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

func (bag SecretsBag) writeToFile(key string, filename string) (bool, error) {
	value, ok := bag.secrets[key]
	if !ok {
		return false, fmt.Errorf("Required secret %v is not found!\n", key)
	}

	var writeNeeded = false

	existing, err := ioutil.ReadFile(filename)
	if err == nil {
		writeNeeded = string(existing) != value
	} else if err == os.ErrNotExist {
		writeNeeded = true
	} else {
		return false, err
	}

	if writeNeeded {
		if err := ioutil.WriteFile(filename, []byte(value), 0600); err != nil {
			return false, err
		}
	}

	return writeNeeded, nil
}
