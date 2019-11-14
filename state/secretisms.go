package state

import (
	"fmt"
	"strings"
)

// ValidateSecretKeys returns an error if any of the keys requested in a set are not loaded in the
// session's SecretBag and nil if all are present.
func (s SessionLease) ValidateSecretKeys(secretKeys []string) error {
	secrets, err := s.GetSecrets()
	if err != nil {
		return err
	}

	missing := make([]string, 0)
	for _, key := range secretKeys {
		if !secrets.Has(key) {
			missing = append(missing, key)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("Unrecognized secret keys: %s", strings.Join(missing, ", "))
	}
	return nil
}

// ListSecretKeys enumerates the known secret keys.
func (s SessionLease) ListSecretKeys() []string {
	bag, err := s.GetSecrets()
	if err != nil {
		return nil
	}
	return bag.Keys()
}

// SetSecrets adds or updates the values associated with many secrets at once, then persists
// them to the database.
func (s SessionLease) SetSecrets(secrets map[string]string) error {
	if len(secrets) == 0 {
		return nil
	}

	bag, err := s.GetSecrets()
	if err != nil {
		return err
	}

	for key, value := range secrets {
		bag.Set(key, value)
	}

	return bag.SaveToDatabase(s.db, s.ring, false)
}

// DeleteSecrets removes the values associated with many secret keys, then persists the changed
// bag to the database.
func (s SessionLease) DeleteSecrets(keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	bag, err := s.GetSecrets()
	if err != nil {
		return err
	}

	for _, key := range keys {
		bag.Delete(key)
	}

	return bag.SaveToDatabase(s.db, s.ring, true)
}
