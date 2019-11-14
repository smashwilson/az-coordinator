package secrets

import (
	"io/ioutil"
	"os"
)

const (
	// FilenameTLSCertificate is the path to the file containing the full chain of public TLS certificates.
	FilenameTLSCertificate = "/etc/ssl/az/backend.azurefire.net/fullchain.pem"

	// FilenameTLSKey is the path to the file containing the TLS private key.
	FilenameTLSKey = "/etc/ssl/az/backend.azurefire.net/privkey.pem"

	// FilenameDHParams is the path to a file containing pre-generated DH parameters.
	FilenameDHParams = "/etc/ssl/az/dhparams.pem"
)

var tlsKeysToPath = map[string]string{
	"TLS_CERTIFICATE": FilenameTLSCertificate,
	"TLS_KEY":         FilenameTLSKey,
	"TLS_DH_PARAMS":   FilenameDHParams,
}

// DesiredTLSFiles constructs a map whose keys are paths on the filesystem and whose values are the contents
// of TLS-related files that are expected to be placed at those paths. An error is returned if any of the
// required TLS secret keys are absent.
func (bag Bag) DesiredTLSFiles() (map[string][]byte, error) {
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

// IsTLSFile returns true if filePath is TLS-related and false if not.
func IsTLSFile(filePath string) bool {
	for _, path := range tlsKeysToPath {
		if path == filePath {
			return true
		}
	}
	return false
}

// ActualTLSFiles constructs a map whose keys are paths on the filesystem and whose values are the actual
// contents of files at those locations on disk. Any file not yet present has a value of nil.
func ActualTLSFiles() (map[string][]byte, error) {
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
