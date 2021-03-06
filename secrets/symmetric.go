package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/kms"
)

// DecoderRing wraps an AWS key management service (KMS) connection with the logic necessary to accomplish
// symmetric encryption backed by KMS-managed shared secrets.
type DecoderRing struct {
	kmsService  *kms.KMS
	masterKeyID string
}

// NewDecoderRing connects to external AWS services.
func NewDecoderRing(masterKeyID, awsRegion string) (*DecoderRing, error) {
	session, err := session.NewSession(&aws.Config{
		Region: &awsRegion,
	})
	if err != nil {
		return nil, err
	}

	kmsService := kms.New(session)
	return &DecoderRing{kmsService: kmsService, masterKeyID: masterKeyID}, nil
}

// Encrypt uses this DecoderRing's master key to generate a one-time encryption key, encrypt the requested
// payload with it, and return ciphertext containing the encrypted key and payload.
func (ring DecoderRing) Encrypt(plaintext string) ([]byte, error) {
	dataKeyResult, err := ring.kmsService.GenerateDataKey(&kms.GenerateDataKeyInput{
		KeyId:   aws.String(ring.masterKeyID),
		KeySpec: aws.String("AES_128"),
	})
	if err != nil {
		return nil, err
	}
	keyPlaintext := dataKeyResult.Plaintext
	keyCiphertext := dataKeyResult.CiphertextBlob

	messagePlaintext := []byte(plaintext)
	block, err := aes.NewCipher(keyPlaintext)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	_, err = io.ReadFull(rand.Reader, nonce)
	if err != nil {
		return nil, err
	}

	messageCiphertext := gcm.Seal(nonce, nonce, messagePlaintext, nil)
	return append(keyCiphertext, messageCiphertext...), nil
}

// Decrypt accepts ciphertext produced by an equivalent DecoderRing's Encrypt method and recovers the original
// plaintext.
func (ring DecoderRing) Decrypt(ciphertext []byte) (*string, error) {
	if len(ciphertext) < 168 {
		return nil, fmt.Errorf("Ciphertext too short: %d", len(ciphertext))
	}

	keyCiphertext := ciphertext[:168]
	decryptResult, err := ring.kmsService.Decrypt(&kms.DecryptInput{
		CiphertextBlob: keyCiphertext,
	})
	if err != nil {
		return nil, err
	}
	keyPlaintext := decryptResult.Plaintext

	block, err := aes.NewCipher(keyPlaintext)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	if len(ciphertext) < 168+gcm.NonceSize() {
		return nil, fmt.Errorf("Ciphertext too short: %d", len(ciphertext))
	}

	nonce := ciphertext[168 : 168+gcm.NonceSize()]
	messageCiphertext := ciphertext[168+gcm.NonceSize():]

	messagePlaintext, err := gcm.Open(nil, nonce, messageCiphertext, nil)
	if err != nil {
		return nil, err
	}
	return aws.String(string(messagePlaintext)), nil
}
